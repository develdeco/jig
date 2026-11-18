package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// newFinalRig is a verify rig whose four slices name literal commands, so the
// union a final run composes is known.
func newFinalRig(t *testing.T) *verifyRig {
	t.Helper()
	r := newVerifyRig(t)
	r.setOracle("a", "echo ran-a")
	r.setOracle("b", "echo ran-b")
	r.setOracle("c", "echo ran-a")
	r.setOracle("d", "echo ran-d")
	r.fixClamp()
	return r
}

// replaceOracle swaps one oracle command of the ticket's slices.yaml for
// another.
func (r *verifyRig) replaceOracle(from, to string) {
	r.t.Helper()
	path := filepath.Join(r.st.TicketDir(r.fx.Ticket), "slices.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	body := strings.Replace(string(data), "oracle: "+from+"\n", "oracle: "+to+"\n", 1)
	if body == string(data) {
		r.t.Fatalf("test setup: no slice names the oracle %q", from)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// TestVerifyFinalRefusesASliceOrACommand: --final is the ticket's own
// commands, so it takes no slice and no command of its own; both are refused
// before anything runs, naming the two flags, and write nothing.
func TestVerifyFinalRefusesASliceOrACommand(t *testing.T) {
	t.Parallel()
	r := newFinalRig(t)
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"a slice", []string{"verify", "--final", "--slice", "a"}, []string{"--final", "--slice"}},
		{"a command", []string{"verify", "--final", "--", "echo", "other"}, []string{"--final", "-- <cmd>"}},
	} {
		out, code := r.run(c.args...)
		if code != 125 || strings.Contains(out, "ran-") {
			t.Errorf("%s: exit = %d, want 125 and nothing run\n%s", c.name, code, out)
		}
		for _, want := range append(c.want, "VALIDATION_ERROR") {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, want, out)
			}
		}
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("journal has %+v, want nothing", lines)
	}
}

// TestVerifyFinalDryRunListsEachSlicesCommand: --dry-run names the union, the
// command each slice contributes and the line it would write, and runs and
// writes nothing.
func TestVerifyFinalDryRunListsEachSlicesCommand(t *testing.T) {
	t.Parallel()
	r := newFinalRig(t)

	out, code := r.run("verify", "--final", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"the union of the ticket's slice commands", "slices[4]{slice,command}:", "a,echo ran-a", "b,echo ran-b", "c,echo ran-a", "d,echo ran-d",
		"(echo ran-a) && (echo ran-b) && (echo ran-d)", "event: final-oracle", "slices: a b c d", "covers: yes", "final: on", "dry-run: on",
		"Nothing ran and nothing was written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ran-a\nran-b") || len(r.journal()) != 0 {
		t.Errorf("a dry run ran or wrote something: %+v\n%s", r.journal(), out)
	}
}

// TestVerifyFinalHeaderAndFooterNameWhatRan: a final run prints the union and
// each slice's command before the output, then how it ended, whether the head
// is covered and what was recorded where, exits with the command's own code,
// and changes no slice's state.
func TestVerifyFinalHeaderAndFooterNameWhatRan(t *testing.T) {
	t.Parallel()
	r := newFinalRig(t)
	sha := gitMust(t, r.fx.RepoDir, "rev-parse", "HEAD")
	sha = strings.TrimSpace(sha)

	out, code := r.run("verify", "--final")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	header, footer, ok := strings.Cut(out, "\nran-a\n")
	if !ok {
		t.Fatalf("output has no command output:\n%s", out)
	}
	for _, want := range []string{"the union of the ticket's slice commands", "(echo ran-a) && (echo ran-b) && (echo ran-d)", "slices[4]{slice,command}:", "commit: " + sha, "tree: clean", "final: on"} {
		if !strings.Contains(header, want) {
			t.Errorf("header lacks %q:\n%s", want, header)
		}
	}
	for _, want := range []string{"ran-b", "ran-d", "outcome: pass", "exit: 0", "seconds: ", "covers: yes", "recorded: final-oracle", "in: the journal of JIG-1", "pushed: "} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer lacks %q:\n%s", want, footer)
		}
	}
	line, ok := journal.FinalRunAt(r.journal(), "", sha)
	if !ok || line.Outcome != "pass" || line.Flags != "--final" || len(line.Slices) != 4 {
		t.Errorf("FinalRunAt = %+v, %v, want the pass the run recorded", line, ok)
	}
	for _, l := range r.journal() {
		if l.Event == journal.EventVerified || l.Event == journal.EventOracle {
			t.Errorf("a final run wrote a %s line", l.Event)
		}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if ss, err := r.st.ReadSliceState(r.fx.Ticket, id); err != nil || ss.State == "green" {
			t.Errorf("slice %s state = %+v, %v, want it untouched by a final run", id, ss, err)
		}
	}

	// A failing command ends the union with its own code, and the line says so.
	r.replaceOracle("echo ran-b", "echo ran-b; exit 7")
	out, code = r.run("verify", "--final", "--json")
	if code != 7 || !strings.Contains(out, `"outcome":"fail"`) || !strings.Contains(out, `"exit":7`) || !strings.Contains(out, `"final":true`) {
		t.Errorf("a failing union: exit = %d, want 7 with a JSON report of the failure\n%s", code, out)
	}
}
