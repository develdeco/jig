// Command jig is the CLI entry point for the jig ticket-solving tool: it
// wires the store, project, session, staircase, frontier and verifydeliver
// packages into a small set of subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/frontier"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mirror"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

func main() {
	gitx.ClearRepoEnv()
	// The first interrupt cancels e.ctx, so a command that waits or streams
	// can wind down; endAfterInterrupt makes sure one that does not watch it
	// still ends, as it did when an interrupt simply killed the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go endAfterInterrupt(ctx, stop, interruptGrace, os.Exit)
	e := processEnv()
	e.ctx = ctx
	os.Exit(run(e, os.Args[1:], os.Stdout, os.Stdin))
}

const (
	// interruptGrace is how long a command has, after the first interrupt,
	// to notice its cancelled context and return.
	interruptGrace = 3 * time.Second
	// exitInterrupted is the status of a run ended by an interrupt: 128 plus
	// SIGINT, the shell convention.
	exitInterrupted = 130
)

// endAfterInterrupt waits for ctx to be cancelled by the first interrupt,
// gives the signal back its default action (stop), so a second interrupt
// ends the process at once, and ends the process itself through exit if the
// run has not returned within grace.
func endAfterInterrupt(ctx context.Context, stop func(), grace time.Duration, exit func(int)) {
	<-ctx.Done()
	stop()
	time.Sleep(grace)
	exit(exitInterrupted)
}

// Main dispatches one CLI invocation against the process's own environment
// and working directory, and returns the process exit code. It is separated
// from main() so a test can drive the binary's entry without spawning a
// subprocess; a test that gives the run an environment of its own calls run.
func Main(args []string, stdout io.Writer, stdin io.Reader) int {
	return run(processEnv(), args, stdout, stdin)
}

// run dispatches one CLI invocation against e and returns the process exit
// code. Everything the invocation reads from outside its arguments and
// streams comes through e (see env), so runs in one process do not touch each
// other's environment.
func run(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		axi.Render(stdout, usageBlocks()...)
		return 0
	}

	v, rest, ok := lookupVerb(args)
	if !ok {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown command %q", args[0]),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig` with no arguments to see the command list"},
		})
	}
	return v.Run(e, rest, stdout, stdin)
}

// renderErr writes err through axi.RenderError (wrapping a plain error in an
// *axi.Error first) and returns its exit code.
func renderErr(stdout io.Writer, err error) int {
	var ae *axi.Error
	if !errors.As(err, &ae) {
		ae = &axi.Error{Msg: err.Error(), Code: "ERROR"}
	}
	axi.RenderError(stdout, ae)
	return axi.ExitCode(ae)
}

// newFlagSet builds a stdlib FlagSet for name that reports parse errors
// without dumping its own usage text (jig's help block covers that), and shows
// it to e.newFlagSetHook when there is one.
func newFlagSet(e env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if e.newFlagSetHook != nil {
		e.newFlagSetHook(name, fs)
	}
	return fs
}

// flagErrDashPatterns match the flag package's parse errors, which always
// name a flag with one dash, so they can be rewritten to jig's "--name" form.
var flagErrDashPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^(flag provided but not defined: )-(\S+)$`),
	regexp.MustCompile(`^(flag needs an argument: )-(\S+)$`),
	regexp.MustCompile(`^(invalid value ".*" for flag )-(\S+?)(:.*)$`),
	regexp.MustCompile(`^(invalid boolean value ".*" for )-(\S+?)(:.*)$`),
}

// normalizeFlagErr renders err's message with flagErrDashPatterns applied.
func normalizeFlagErr(err error) string {
	msg := err.Error()
	for _, re := range flagErrDashPatterns {
		if m := re.FindStringSubmatch(msg); m != nil {
			out := m[1] + "--" + m[2]
			if len(m) == 4 {
				out += m[3]
			}
			return out
		}
	}
	return msg
}

// parseFlags parses args against fs. For "-h"/"--help" it prints the
// command's flags and reports handled, so the caller exits 0; a parse failure
// comes back as a VALIDATION_ERROR.
func parseFlags(stdout io.Writer, fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			axi.Render(stdout, flagsBlockFor(fs.Name())...)
			return true, nil
		}
		return false, &axi.Error{Msg: normalizeFlagErr(err), Code: "VALIDATION_ERROR"}
	}
	return false, nil
}

// requirePositional extracts args[0] as a required positional (e.g. a
// ticket id): present and not itself a flag. A leading "-h"/"--help" is
// passed through so the command prints its help instead.
func requirePositional(args []string, what string) (string, []string, error) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		return "", args, nil
	}
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return "", nil, &axi.Error{
			Msg:  fmt.Sprintf("missing required %s", what),
			Code: "VALIDATION_ERROR",
		}
	}
	return args[0], args[1:], nil
}

// extractAnswer pulls a "--answer <qid> <text>" pair (or "-answer") out of
// args wherever it appears, since it takes two positional-shaped values and
// stdlib flag only ever consumes one value per flag. It returns the
// remaining args with that flag and its two values removed.
func extractAnswer(args []string) (qid, text string, rest []string, err error) {
	for i, a := range args {
		if a != "--answer" && a != "-answer" {
			continue
		}
		if i+2 >= len(args) {
			return "", "", nil, &axi.Error{
				Msg:  "--answer requires two values: <qid> <text>",
				Code: "VALIDATION_ERROR",
			}
		}
		qid, text = args[i+1], args[i+2]
		rest = make([]string, 0, len(args)-3)
		rest = append(rest, args[:i]...)
		rest = append(rest, args[i+3:]...)
		return qid, text, rest, nil
	}
	return "", "", args, nil
}

func splitOnce(s string, sep byte) (before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// resolveStore resolves the store, project config and this machine's clone
// mapping for cfg's project, honoring an explicit --store flag over cwd
// resolution (locate.Locate's order: a jig.yaml marker, the machine map by
// path and by remote, a store directory), and returns the jig home root it
// read the mapping from, for
// the packages that keep leases under it. stdout is the resolving command's
// own writer: the checkpoint hook renders its sync report through it
// (brief.md#Ownership and drift: "Drift lines print in the output of the
// command whose checkpoint found them"), and a failed sync's warning lands
// in the command's own structured output rather than bare stderr.
func resolveStore(e env, storeFlag string, stdout io.Writer) (*store.Store, project.Config, project.MachineProject, string, error) {
	return resolveStoreChecked(e, storeFlag, stdout, false)
}

// resolveStoreChecked is resolveStore's and
// resolveStoreForProjectAllowingOldSchema's shared implementation:
// allowOlderSchema says whether a schema_version older than
// project.CurrentSchemaVersion passes the one schema check every command
// loading project.yaml runs here, rather than each command checking it
// itself. `jig store migrate` is the one command that passes true: its whole
// job is opening such a store and rewriting it onto the current schema, the
// one exemption project.Config.CheckSchemaVersion takes besides `jig help`
// and `jig version`, neither of which resolves a store at all.
func resolveStoreChecked(e env, storeFlag string, stdout io.Writer, allowOlderSchema bool) (*store.Store, project.Config, project.MachineProject, string, error) {
	jigHome, err := e.jigHome()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	cwd, err := e.getwd()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	storePath, cfg, err := locateStore(jigHome, cwd, absFrom(cwd, storeFlag), e.getenv)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	if err := cfg.CheckSchemaVersion(allowOlderSchema); err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	st, err := store.Open(storePath)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	// Every store checkpoint (Store.Push) syncs the GitHub mirror, from
	// inside the command that made it: the mirror depends on the store
	// (internal/mirror imports internal/store), never the reverse, so this
	// is the one place that wires the hook rather than internal/store
	// importing internal/mirror itself. A store with no trackers: github:
	// entry is a no-op (mirror.Sync's own NoTracker report), so this never
	// reaches the network for the vast majority of stores and tests.
	st.AfterCheckpoint = func(s *store.Store) error {
		report, err := mirror.Sync(mirror.Deps{Store: s, Cfg: cfg, Home: jigHome, Client: e.mirrorClient}, mirror.SyncOpts{})
		if err != nil {
			return err
		}
		renderSyncReport(stdout, report, false)
		return nil
	}
	st.Warn = func(format string, args ...any) {
		axi.Render(stdout, axi.KV("warning", [][2]string{{"message", fmt.Sprintf(format, args...)}}))
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	return st, cfg, machine[cfg.Name], jigHome, nil
}

// resolveStoreForProject resolves the store, project config and machine
// mapping the same way resolveStore does, except an explicit --project name
// is tried first: it resolves the store path via the per-machine project
// mapping (project.LoadMachine(jigHome)[name].Store), ahead of the
// --store/cwd fallback chain resolveStore falls through to when projectFlag
// is empty.
func resolveStoreForProject(e env, projectFlag, storeFlag string, stdout io.Writer) (*store.Store, project.Config, project.MachineProject, string, error) {
	return resolveStoreForProjectChecked(e, projectFlag, storeFlag, stdout, false)
}

// resolveStoreForProjectAllowingOldSchema is resolveStoreForProject with
// resolveStoreForMigrate's own exemption: `jig store migrate` takes --project
// too, so it needs the --project resolution path, not only the --store/cwd
// one resolveStoreForMigrate covers alone.
func resolveStoreForProjectAllowingOldSchema(e env, projectFlag, storeFlag string, stdout io.Writer) (*store.Store, project.Config, project.MachineProject, string, error) {
	return resolveStoreForProjectChecked(e, projectFlag, storeFlag, stdout, true)
}

func resolveStoreForProjectChecked(e env, projectFlag, storeFlag string, stdout io.Writer, allowOlderSchema bool) (*store.Store, project.Config, project.MachineProject, string, error) {
	if projectFlag == "" {
		return resolveStoreChecked(e, storeFlag, stdout, allowOlderSchema)
	}
	jigHome, err := e.jigHome()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, "", err
	}
	mp, ok := machine[projectFlag]
	if !ok || mp.Store == "" {
		return nil, project.Config{}, project.MachineProject{}, "", &axi.Error{
			Msg:  fmt.Sprintf("no project %q in the machine mapping", projectFlag),
			Code: "VALIDATION_ERROR",
		}
	}
	return resolveStoreChecked(e, mp.Store, stdout, allowOlderSchema)
}

// rungs returns cfg's staircase rungs, falling back to staircase.Default()
// when the project has not declared any.
func rungs(cfg project.Config) staircase.Config {
	if len(cfg.Staircase) > 0 {
		return staircase.Config{Rungs: cfg.Staircase}
	}
	return staircase.Default()
}

// backendName resolves the effective session backend name: an explicit
// --backend flag wins; otherwise "fake" when --scenario was given, else
// "herdr".
func backendName(explicit, scenario string) string {
	if explicit != "" {
		return explicit
	}
	if scenario != "" {
		return "fake"
	}
	return "herdr"
}

// journalFunc binds a frontier.Deps-shaped journal callback to st and ticket.
func journalFunc(st *store.Store, ticket string) func(journal.Line) error {
	return func(l journal.Line) error {
		return journal.Append(st, ticket, l)
	}
}

// frontierDeps assembles frontier.Deps for one ticket.
func frontierDeps(st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string, backend session.Backend, ticket string) frontier.Deps {
	return frontier.Deps{
		Store:   st,
		Cfg:     cfg,
		Machine: mp,
		Backend: backend,
		Rungs:   rungs(cfg),
		Journal: journalFunc(st, ticket),
		Home:    jigHome,
	}
}

// verifydeliverDeps assembles verifydeliver.Deps for one ticket. Unlike
// frontier.Deps, verifydeliver.Deps carries no Backend or injected Journal
// func: Gate and Publish journal internally, bound to d.Store.
//
// It also resolves the operator's own home directory, once, beside the jig
// home root it is handed: gate intent inference looks for their local agent
// transcripts under it, on every command that gates (jig gate and jig
// solve). e.userHomeDir fails when it cannot say, which leaves UserHome ""
// and inference reports that as its reason instead of looking anywhere else.
func verifydeliverDeps(e env, st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string) verifydeliver.Deps {
	userHome, _ := e.userHomeDir()
	return verifydeliver.Deps{
		Store:    st,
		Cfg:      cfg,
		Machine:  mp,
		Rungs:    rungs(cfg),
		Home:     jigHome,
		UserHome: userHome,
	}
}

// idRows turns a slice of ids into single-column table rows.
func idRows(ids []string) [][]string {
	rows := make([][]string, len(ids))
	for i, id := range ids {
		rows[i] = []string{id}
	}
	return rows
}

// hintOrFallback computes the contextual next-step hint for ticket, falling
// back to a generic status pointer if the hint computation itself fails
// (e.g. a transient disk error reading questions/state).
func hintOrFallback(st *store.Store, ticket string) string {
	hint, err := nextStepHint(st, ticket)
	if err != nil {
		return fmt.Sprintf("Run `jig status %s` to see slice states", ticket)
	}
	return hint
}
