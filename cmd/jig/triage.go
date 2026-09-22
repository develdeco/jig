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
// verifydeliver.DefaultTriage and prints one note line explaining why;
// a terminal stdin runs the interactive batch/per-ask prompt.
func triageFor(yes bool, stdin io.Reader, stdout io.Writer) verifydeliver.Triage {
	return func(in verifydeliver.TriageInput) verifydeliver.TriageResult {
		switch {
		case yes:
			fmt.Fprintln(stdout, "triage: kept every fix and workspace ask (--yes)")
			return verifydeliver.DefaultTriage(in)
		case !stdinIsTerminal(stdin):
			fmt.Fprintln(stdout, "triage: kept every fix and workspace ask (stdin is not a terminal)")
			return verifydeliver.DefaultTriage(in)
		default:
			return interactiveTriage(in, stdin, stdout)
		}
	}
}

// interactiveTriage runs design 6.4's terminal flow over a single
// bufio.Reader (plain line-wise prompts, no raw mode):
//   - notes are listed for information only.
//   - fixes are listed sorted by risk with their rationale, then one batch
//     prompt: Enter accepts every fix; a space/comma separated list of ids
//     dismisses just those; an unknown id reprompts the whole line.
//   - each ask prompts individually: "d"/"dismiss" dismisses it; anything
//     else keeps it, and that text (when not just "k"/"keep"/empty)
//     becomes its decision. A no-workspace ask additionally prompts for a
//     manifest workspace id before it can be kept (Q1).
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
			rows[i] = []string{f.ID, f.Risk, f.Title}
		}
		axi.Render(stdout, axi.Table("notes", []string{"id", "risk", "title"}, rows))
	}

	eof := false
	if len(in.Fixes) > 0 {
		rows := make([][]string, len(in.Fixes))
		for i, f := range in.Fixes {
			rows[i] = []string{f.ID, f.Risk, f.Title, f.RiskRationale}
		}
		axi.Render(stdout, axi.Table("fixes", []string{"id", "risk", "title", "risk_rationale"}, rows))

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
		for {
			fmt.Fprintf(stdout, "%s [%s] %s - keep (with an optional decision) or dismiss? [<decision text>|d]: ", f.ID, f.Risk, f.Title)
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
			lower := strings.ToLower(trimmed)
			if lower == "d" || lower == "dismiss" {
				result.Asks[f.ID] = verifydeliver.AskOutcome{Keep: false, Human: true}
				continue askLoop
			}
			decision := ""
			if lower != "" && lower != "k" && lower != "keep" {
				decision = trimmed
			}
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
			continue askLoop
		}
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
