package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig triage`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	triageReasonUsage    = "why, in a sentence: carried into the fix slice's goal when the ask is kept"
	triageWorkspaceUsage = "the workspace to build the fix in, for a kept ask whose file lies in none"
	triageOracleUsage    = "the oracle that proves the fix, for a kept ask the reviewer named none for"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"triage", "keep or dismiss an ask a gate round left for a person to decide", []flagSpec{
			{"reason", triageReasonUsage, false},
			{"workspace", triageWorkspaceUsage, false},
			{"oracle", triageOracleUsage, false},
			{"store", reviewStoreUsage, false},
			{"project", reviewProjectUsage, false},
		}, false},
		Order: 333,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdTriage(e, args, stdout, stdin)
		},
		Probe: []string{"T-1", "r1-f1", "keep"},
	})
}

// fileLine renders a finding's location as "file:line" for every place a
// finding reaches a human: the gate report's findings table and
// needs_a_human, `jig status`'s asks.
func fileLine(f verifydeliver.Finding) string {
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

// stdinIsTerminal reports whether r is an interactive terminal. It is true
// only for an *os.File that is a real console/tty, decided by an OS
// terminal query (isTerminalFile, build-tagged per GOOS: GetConsoleMode on
// windows, a termios ioctl on linux/darwin/freebsd/netbsd/openbsd/
// dragonfly, unconditionally false elsewhere). A pipe, a regular file, the
// null device (/dev/null, NUL - itself a character device on every OS, so
// a mode-bit check alone cannot tell it apart from a real tty), and any
// non-*os.File reader (a bytes.Buffer, a strings.Reader) are all not a
// terminal. It is env.stdinIsTerminal's production value; a test puts a scripted
// stub there instead.
func stdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(f)
}

// stdoutIsTerminal reports whether w is an interactive terminal, by the same
// rule as stdinIsTerminal: only an *os.File that is a real console/tty is, so
// a pipe, a file, the null device and any in-memory writer are not. It is
// env.stdoutIsTerminal's production value.
func stdoutIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(f)
}

// triageFor builds the GateOpts.Triage hook the scripted gate (`jig gate
// --scenario`) wires in. jig prompts nobody: verifydeliver.DefaultTriage keeps every fix and
// every ask with a full build target, and an ask missing a part of it stays
// undecided for a person to decide with `jig triage`. It prints one note line
// naming why nothing was prompted, only when there was something to triage.
func triageFor(_ env, yes bool, _ io.Reader, stdout io.Writer) verifydeliver.Triage {
	return func(in verifydeliver.TriageInput) verifydeliver.TriageResult {
		why := "jig does not prompt; decide asks with `jig triage`"
		if yes {
			why = "--yes"
		}
		return defaultTriageWithNote(in, stdout, why)
	}
}

// defaultTriageWithNote runs verifydeliver.DefaultTriage and, only when this
// round actually had a fix or ask to triage, prints one line saying why
// nothing was prompted for (why is "--yes", say). A note is never triaged, so
// a notes-only round prints no note either, and a round that routed nothing at
// all (e.g. a dispatched reviewer round that turned up nothing new) prints
// none. It never claims an ask was kept when DefaultTriage actually left it
// undecided: undecided is counted straight from what DefaultTriage's own
// result leaves out of Asks, rather than re-deriving DefaultTriage's own
// workspace/oracle rule here, so the two can never drift apart.
func defaultTriageWithNote(in verifydeliver.TriageInput, stdout io.Writer, why string) verifydeliver.TriageResult {
	if len(in.Fixes) == 0 && len(in.Asks) == 0 {
		return verifydeliver.DefaultTriage(in)
	}
	result := verifydeliver.DefaultTriage(in)
	undecided := 0
	for _, f := range in.Asks {
		if _, decided := result.Asks[f.ID]; !decided {
			undecided++
		}
	}
	if undecided > 0 {
		fmt.Fprintf(stdout, "triage (%s): kept every fix and buildable ask; %d ask(s) left for a human\n", why, undecided)
	} else {
		fmt.Fprintf(stdout, "triage (%s): kept every fix and buildable ask\n", why)
	}
	return result
}

// cmdTriage implements `jig triage [T] <finding-id> keep|dismiss [--reason R]
// [--workspace W] [--oracle O]`: a person's decision on an ask a gate round
// left undecided, journaled as a triage line. Nothing is prompted and no round
// is opened: the next round applies the decision, turning a kept ask into a
// fix slice and a dismissed one into a dismissed finding. Who decided is
// recorded: a person at a terminal, or an agent.
func cmdTriage(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "triage")
	reason := fs.String("reason", "", triageReasonUsage)
	workspace := fs.String("workspace", "", triageWorkspaceUsage)
	oracle := fs.String("oracle", "", triageOracleUsage)
	storeFlag := fs.String("store", "", reviewStoreUsage)
	projectFlag := fs.String("project", "", reviewProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	usage := &axi.Error{
		Msg:  "jig triage takes [ticket] <finding-id> keep|dismiss",
		Code: "VALIDATION_ERROR",
		Help: []string{"For example: `jig triage T-1 r1-f2 keep --reason \"yes, fix it\"`"},
	}
	var ticketArg, id, decision string
	switch len(positionals) {
	case 2:
		id, decision = positionals[0], positionals[1]
	case 3:
		ticketArg, id, decision = positionals[0], positionals[1], positionals[2]
	default:
		return renderErr(stdout, usage)
	}
	if decision != verifydeliver.TriageKeep && decision != verifydeliver.TriageDismiss {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("%q is not a decision: say keep or dismiss", decision),
			Code: "VALIDATION_ERROR",
			Help: usage.Help,
		})
	}
	if decision == verifydeliver.TriageDismiss && (*workspace != "" || *oracle != "") {
		return renderErr(stdout, &axi.Error{
			Msg:  "--workspace and --oracle choose where a kept ask is built; a dismissed one is not built",
			Code: "VALIDATION_ERROR",
		})
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}
	st, _, _, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	var tpos []string
	if ticketArg != "" {
		tpos = []string{ticketArg}
	}
	ticket, err := reviewTicket(e, st, jigHome, tpos, "triage", stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	asks, _, err := verifydeliver.OutstandingAsks(st, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	var ids []string
	found := false
	for _, f := range asks {
		ids = append(ids, f.ID)
		found = found || f.ID == id
	}
	if !found {
		listed := "it has none"
		if len(ids) > 0 {
			listed = "its asks are " + strings.Join(ids, ", ")
		}
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("%s is not an outstanding ask of %s (%s)", id, ticket, listed),
			Code: "TRIAGE_NOT_AN_ASK",
			Help: []string{fmt.Sprintf("`jig status %s` lists the asks a person has to decide", ticket)},
		})
	}

	provenance := "agent"
	if e.stdinIsTerminal(stdin) {
		provenance = "person"
	}
	line := journal.Line{
		Event: journal.EventTriage, Ref: id, Outcome: decision, Provenance: provenance,
		Summary: oneLineText(*reason), Command: verifydeliver.TriageTarget(*workspace, *oracle),
		Session: envSession(e),
	}
	if err := journal.Append(st, ticket, line); err != nil {
		return renderErr(stdout, err)
	}
	if err := st.Push(fmt.Sprintf("%s: triage %s %s", ticket, id, decision)); err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("triage", [][2]string{{"ticket", ticket}, {"finding", id}, {"decision", decision}, {"provenance", provenance}}),
		axi.Help(fmt.Sprintf("The next round applies it: run `jig gate %s`", ticket)),
	)
	return 0
}

// oneLineText flattens text to a single line of at most 600 bytes, the shape
// of a journal summary.
func oneLineText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 600 {
		text = text[:600]
	}
	return text
}
