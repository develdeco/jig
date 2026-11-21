package main

import (
	"strings"
	"testing"
)

// TestSuiteRunFooterSaysTheCIAndTheHeaderTheSuite: an issue's final run names
// the suite oracle, lists no slices and ends with the CI line; a repo without
// a suite ends unrun saying so.
func TestSuiteRunFooterSaysTheCIAndTheHeaderTheSuite(t *testing.T) {
	t.Parallel()
	r := newFinalRig(t)
	if err := r.st.SetTicketKind(r.fx.Ticket, "issue", ""); err != nil {
		t.Fatal(err)
	}
	out, code := r.run("verify", "--final")
	if code != 125 || !strings.Contains(out, "no suite declared in") || strings.Contains(out, "ran-") {
		t.Fatalf("exit = %d, want 125 naming the missing suite\n%s", code, out)
	}

	decl := "workspaces:\n  - id: root\n    path: .\noracles:\n  suite: \"echo ran-suite\"\n"
	r.commit("", "declare suite", map[string]string{".claude/jig.yaml": decl})
	out, code = r.run("verify", "--final")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"the repo's suite oracle", "ran-suite", "outcome: pass", "covers: yes", "need not run the whole suite in CI"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "slices[") {
		t.Errorf("an issue's run lists slices:\n%s", out)
	}
}
