// Command jig is the CLI entry point for the jig ticket-solving tool: it
// wires the store, project, session, staircase, make and verifydeliver
// packages into a small set of subcommands.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/journal"
	makepkg "github.com/develdeco/jig/make"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/session"
	"github.com/develdeco/jig/staircase"
	"github.com/develdeco/jig/store"
	"github.com/develdeco/jig/verifydeliver"
)

func main() {
	os.Exit(Main(os.Args[1:], os.Stdout, os.Stdin))
}

// Main dispatches one CLI invocation and returns the process exit code. It
// is separated from main() so tests can drive it without spawning a
// subprocess.
func Main(args []string, stdout io.Writer, stdin io.Reader) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		axi.Render(stdout, usageBlocks()...)
		return 0
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return cmdInit(rest, stdout)
	case "ticket":
		return cmdTicket(rest, stdout)
	case "solve":
		return cmdSolve(rest, stdout)
	case "run":
		return cmdRun(rest, stdout)
	case "requeue":
		return cmdRequeue(rest, stdout)
	case "gate":
		return cmdGate(rest, stdout)
	case "publish":
		return cmdPublish(rest, stdout)
	case "status":
		return cmdStatus(rest, stdout)
	case "validate":
		return cmdValidate(rest, stdout)
	case "version":
		return cmdVersion(rest, stdout)
	case "skills":
		return cmdSkills(rest, stdout)
	case "_screen":
		return cmdScreen(stdin, stdout)
	default:
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown command %q", cmd),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig` with no arguments to see the command list"},
		})
	}
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
// without dumping its own usage text (jig's help block covers that).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// requirePositional extracts args[0] as a required positional (e.g. a
// ticket id): present and not itself a flag.
func requirePositional(args []string, what string) (string, []string, error) {
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

// cloneFlag is a repeatable "--clone name=path" flag.Value.
type cloneFlag struct{ m map[string]string }

func (c *cloneFlag) String() string { return "" }

func (c *cloneFlag) Set(v string) error {
	name, path, ok := splitOnce(v, '=')
	if !ok || name == "" || path == "" {
		return fmt.Errorf("--clone must be name=path, got %q", v)
	}
	if c.m == nil {
		c.m = map[string]string{}
	}
	c.m[name] = path
	return nil
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
// resolution.
func resolveStore(storeFlag string) (*store.Store, project.Config, project.MachineProject, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, err
	}
	storePath, cfg, err := project.Resolve(cwd, storeFlag)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, err
	}
	st, err := store.Open(storePath)
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, err
	}
	machine, err := project.LoadMachine()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, err
	}
	return st, cfg, machine[cfg.Name], nil
}

// resolveStoreForProject resolves the store, project config and machine
// mapping the same way resolveStore does, except an explicit --project name
// is tried first: it resolves the store path via the per-machine project
// mapping (project.LoadMachine()[name].Store), ahead of the --store/cwd
// fallback chain resolveStore falls through to when projectFlag is empty.
func resolveStoreForProject(projectFlag, storeFlag string) (*store.Store, project.Config, project.MachineProject, error) {
	if projectFlag == "" {
		return resolveStore(storeFlag)
	}
	machine, err := project.LoadMachine()
	if err != nil {
		return nil, project.Config{}, project.MachineProject{}, err
	}
	mp, ok := machine[projectFlag]
	if !ok || mp.Store == "" {
		return nil, project.Config{}, project.MachineProject{}, &axi.Error{
			Msg:  fmt.Sprintf("no project %q in the machine mapping", projectFlag),
			Code: "VALIDATION_ERROR",
		}
	}
	return resolveStore(mp.Store)
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

// journalFunc binds a make.Deps-shaped journal callback to st and ticket.
func journalFunc(st *store.Store, ticket string) func(journal.Line) error {
	return func(l journal.Line) error {
		return journal.Append(st, ticket, l)
	}
}

// makeDeps assembles make.Deps for one ticket.
func makeDeps(st *store.Store, cfg project.Config, mp project.MachineProject, backend session.Backend, ticket string) makepkg.Deps {
	return makepkg.Deps{
		Store:   st,
		Cfg:     cfg,
		Machine: mp,
		Backend: backend,
		Rungs:   rungs(cfg),
		Journal: journalFunc(st, ticket),
	}
}

// verifydeliverDeps assembles verifydeliver.Deps for one ticket. Unlike
// make.Deps, verifydeliver.Deps carries no Backend or injected Journal
// func: Gate and Publish journal internally, bound to d.Store.
func verifydeliverDeps(st *store.Store, cfg project.Config, mp project.MachineProject) verifydeliver.Deps {
	return verifydeliver.Deps{
		Store:   st,
		Cfg:     cfg,
		Machine: mp,
		Rungs:   rungs(cfg),
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
