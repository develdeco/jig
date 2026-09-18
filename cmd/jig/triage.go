package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// stdinIsTerminal reports whether r is an interactive terminal. It is true
// only for an *os.File whose Stat mode has os.ModeCharDevice; anything else
// (a pipe, a bytes.Buffer, a strings.Reader) is not a terminal. Tests
// override this var with a scripted stub.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// triageFor builds the GateOpts.Triage hook cmdGate and cmdSolve wire in:
// --yes or a non-terminal stdin keeps every finding, printing one note
// line; a terminal stdin runs the interactive per-finding prompt.
func triageFor(yes bool, stdin io.Reader, stdout io.Writer) func([]verifydeliver.Finding) []verifydeliver.Finding {
	return func(findings []verifydeliver.Finding) []verifydeliver.Finding {
		switch {
		case yes:
			fmt.Fprintf(stdout, "triage: kept all %d findings (--yes)\n", len(findings))
			return findings
		case !stdinIsTerminal(stdin):
			fmt.Fprintf(stdout, "triage: kept all %d findings (stdin is not a terminal)\n", len(findings))
			return findings
		default:
			return interactiveTriage(findings, stdin, stdout)
		}
	}
}

// interactiveTriage prints the findings table, then prompts once per
// finding over a single bufio.Reader (plain line-wise prompts, no raw
// mode): y/yes/empty keeps, n/no dismisses, "A"/"a" keeps this and every
// remaining finding, "N" (case-sensitive) dismisses this and every
// remaining finding, anything else reprompts, and EOF keeps this and every
// remaining finding. It ends with a summary line.
func interactiveTriage(findings []verifydeliver.Finding, stdin io.Reader, stdout io.Writer) []verifydeliver.Finding {
	rows := make([][]string, len(findings))
	for i, f := range findings {
		rows[i] = []string{f.ID, f.Class, f.Workspace, f.Title}
	}
	axi.Render(stdout, axi.Table("findings", []string{"id", "class", "workspace", "title"}, rows))

	r := bufio.NewReader(stdin)
	var kept []verifydeliver.Finding
	mode := "" // "" = ask each one; "keep"/"dismiss" = apply to every remaining finding
	for _, f := range findings {
		switch mode {
		case "keep":
			kept = append(kept, f)
			continue
		case "dismiss":
			continue
		}

		for {
			fmt.Fprintf(stdout, "%s [%s] %s - keep? [y]es / [n]o dismiss / [A] keep rest / [N] dismiss rest: ", f.ID, f.Class, f.Title)
			line, err := r.ReadString('\n')
			if err != nil && strings.TrimSpace(line) == "" {
				fmt.Fprintln(stdout, "triage: stdin closed; keeping this and every remaining finding")
				kept = append(kept, f)
				mode = "keep"
				break
			}
			switch strings.TrimSpace(line) {
			case "y", "yes", "":
				kept = append(kept, f)
			case "n", "no":
				// dismissed: not added to kept
			case "A", "a":
				kept = append(kept, f)
				mode = "keep"
			case "N":
				mode = "dismiss"
			default:
				continue
			}
			break
		}
	}
	fmt.Fprintf(stdout, "triage: kept %d of %d findings\n", len(kept), len(findings))
	return kept
}
