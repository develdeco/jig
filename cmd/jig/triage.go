package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// fileLine renders a finding's location as "file:line" for every place a
// finding reaches a human (design 6.4): the triage prompt, the notes table,
// the gate report's findings table and needs_a_human.
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
// terminal. Tests override this var with a scripted stub.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(f)
}

// triageFor builds the GateOpts.Triage hook cmdGate and cmdSolve wire in
// (design 6.4, D-1, D-2): --yes or a non-terminal stdin runs
// verifydeliver.DefaultTriage and, only when there was something to triage,
// prints one note line explaining why nothing was prompted; a terminal
// stdin runs the interactive batch/per-ask prompt.
func triageFor(yes bool, stdin io.Reader, stdout io.Writer) verifydeliver.Triage {
	return func(in verifydeliver.TriageInput) verifydeliver.TriageResult {
		switch {
		case yes:
			return defaultTriageWithNote(in, stdout, "--yes")
		case !stdinIsTerminal(stdin):
			return defaultTriageWithNote(in, stdout, "stdin is not a terminal")
		default:
			return interactiveTriage(in, stdin, stdout)
		}
	}
}

// defaultTriageWithNote runs verifydeliver.DefaultTriage and, only when this
// round actually had a fix, ask or note to triage, prints one line saying
// why nothing was prompted for (why is "--yes" or "stdin is not a
// terminal"). A round that routed nothing (e.g. a dispatched reviewer round
// that turned up nothing new) prints no note at all. It never claims a
// no-workspace ask was kept: DefaultTriage leaves those undecided (Q1), so
// the line instead says how many are left for a human to decide.
func defaultTriageWithNote(in verifydeliver.TriageInput, stdout io.Writer, why string) verifydeliver.TriageResult {
	total := len(in.Fixes) + len(in.Asks) + len(in.Notes)
	if total == 0 {
		return verifydeliver.DefaultTriage(in)
	}
	undecided := 0
	for _, f := range in.Asks {
		if f.Workspace == "" {
			undecided++
		}
	}
	if undecided > 0 {
		fmt.Fprintf(stdout, "triage: kept every fix and workspace ask; %d ask(s) with no workspace left for a human (%s)\n", undecided, why)
	} else {
		fmt.Fprintf(stdout, "triage: kept every fix and workspace ask (%s)\n", why)
	}
	return verifydeliver.DefaultTriage(in)
}

// interactiveTriage runs design 6.4's terminal flow over a single
// bufio.Reader (plain line-wise prompts, no raw mode):
//   - notes are listed for information only.
//   - fixes are listed sorted by risk with their rationale, then one batch
//     prompt: Enter accepts every fix; a space/comma separated list of ids
//     dismisses just those; an unknown id reprompts the whole line.
//   - each ask prompts individually, keep or dismiss only: "n"/"no"/
//     "d"/"dismiss" dismisses it; only an explicit "k"/"keep"/Enter keeps
//     it, and a kept ask is then asked for its decision text on its own
//     prompt (optional, Enter skips it). A no-workspace ask additionally
//     prompts for a manifest workspace id before it can be kept (Q1).
//   - stdin closing at any point keeps every remaining fix and every
//     remaining ask that already has a workspace (auto, not human); a
//     remaining no-workspace ask stays undecided (Q1), since keeping it
//     needs a judgment nothing here can supply once stdin is gone.
func interactiveTriage(in verifydeliver.TriageInput, stdin io.Reader, stdout io.Writer) verifydeliver.TriageResult {
	r := bufio.NewReader(stdin)
	result := verifydeliver.TriageResult{
		DismissedFixIDs: map[string]bool{},
		Asks:            map[string]verifydeliver.AskOutcome{},
	}

	if len(in.Notes) > 0 {
		rows := make([][]string, len(in.Notes))
		for i, f := range in.Notes {
			rows[i] = []string{f.ID, f.Risk, fileLine(f), f.Title, f.RiskRationale}
		}
		axi.Render(stdout, axi.Table("notes", []string{"id", "risk", "file:line", "title", "risk_rationale"}, rows))
	}

	eof := false
	if len(in.Fixes) > 0 {
		rows := make([][]string, len(in.Fixes))
		for i, f := range in.Fixes {
			rows[i] = []string{f.ID, f.Risk, fileLine(f), f.Title, f.RiskRationale}
		}
		axi.Render(stdout, axi.Table("fixes", []string{"id", "risk", "file:line", "title", "risk_rationale"}, rows))

		validIDs := map[string]bool{}
		for _, f := range in.Fixes {
			validIDs[f.ID] = true
		}
		for {
			fmt.Fprintf(stdout, "fixes: press Enter to accept all %d, or list ids to dismiss: ", len(in.Fixes))
			line, err := r.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if err != nil && trimmed == "" {
				eof = true
				fmt.Fprintln(stdout, "triage: stdin closed; keeping every remaining fix and workspace ask")
				break
			}
			if trimmed == "" {
				result.FixHuman = true
				break
			}
			ids := strings.FieldsFunc(trimmed, func(r rune) bool { return r == ',' || r == ' ' })
			bad := ""
			for _, id := range ids {
				if !validIDs[id] {
					bad = id
					break
				}
			}
			if bad != "" {
				fmt.Fprintf(stdout, "unknown fix id %q; try again\n", bad)
				continue
			}
			for _, id := range ids {
				result.DismissedFixIDs[id] = true
			}
			result.FixHuman = true
			break
		}
	}

askLoop:
	for _, f := range in.Asks {
		if eof {
			if f.Workspace != "" {
				result.Asks[f.ID] = verifydeliver.AskOutcome{Keep: true, Workspace: f.Workspace}
			}
			continue
		}

		// design 6.4: every finding reaching the human seam is shown with
		// file:line, detail and risk rationale, not the title alone.
		fmt.Fprintf(stdout, "%s [%s] %s %s\n", f.ID, f.Risk, fileLine(f), f.Title)
		if f.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", f.Detail)
		}
		fmt.Fprintf(stdout, "  risk (%s): %s\n", f.Risk, f.RiskRationale)

		keep := false
		for {
			fmt.Fprint(stdout, "keep or dismiss? [k/keep/Enter=keep, n/no/d/dismiss=dismiss]: ")
			line, err := r.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if err != nil && trimmed == "" {
				eof = true
				fmt.Fprintln(stdout, "triage: stdin closed; keeping every remaining fix and workspace ask")
				if f.Workspace != "" {
					result.Asks[f.ID] = verifydeliver.AskOutcome{Keep: true, Workspace: f.Workspace}
				}
				continue askLoop
			}
			switch strings.ToLower(trimmed) {
			case "", "k", "keep":
				keep = true
			case "n", "no", "d", "dismiss":
				keep = false
			default:
				fmt.Fprintln(stdout, "please answer keep (k, keep, or Enter) or dismiss (n, no, d, or dismiss)")
				continue
			}
			break
		}

		if !keep {
			result.Asks[f.ID] = verifydeliver.AskOutcome{Keep: false, Human: true}
			continue askLoop
		}

		fmt.Fprintf(stdout, "decision for %s (optional, Enter to skip): ", f.ID)
		line, _ := r.ReadString('\n')
		decision := strings.TrimSpace(line)

		ws := f.Workspace
		if ws == "" {
			got, wsEOF := promptWorkspace(in.Manifest, r, stdout)
			if wsEOF {
				eof = true
				fmt.Fprintln(stdout, "triage: stdin closed; keeping every remaining fix and workspace ask")
				// f cannot be kept without a workspace judgment (Q1);
				// leave it undecided rather than build a slice with no
				// build target.
				continue askLoop
			}
			ws = got
		}
		result.Asks[f.ID] = verifydeliver.AskOutcome{Keep: true, Decision: decision, Workspace: ws, Human: true}
	}

	return result
}

// promptWorkspace asks for one of man's declared workspace ids (Q1: a kept
// ask whose file lies in no declared workspace needs the human's choice
// before it can become a fix slice). eof is true when stdin closed before
// a valid id arrived.
func promptWorkspace(man manifest.Manifest, r *bufio.Reader, stdout io.Writer) (id string, eof bool) {
	ids := make([]string, len(man.Workspaces))
	valid := map[string]bool{}
	for i, ws := range man.Workspaces {
		ids[i] = ws.ID
		valid[ws.ID] = true
	}
	for {
		fmt.Fprintf(stdout, "this finding's file is in no declared workspace; pick one [%s]: ", strings.Join(ids, ","))
		line, err := r.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if err != nil && trimmed == "" {
			return "", true
		}
		if valid[trimmed] {
			return trimmed, false
		}
		fmt.Fprintf(stdout, "unknown workspace %q; try again\n", trimmed)
	}
}
