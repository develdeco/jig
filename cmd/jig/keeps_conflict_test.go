package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestKeepsConflictAdviceIsShownByGateAndPublishBeforeTheirWork: a ticket
// whose branch cannot merge with the branch of another open ticket is told
// so, with the conflicted file and the stacking command, by `jig here`, `jig
// next`, `jig gate` and `jig publish`; a pair that merges cleanly gets only an
// info line in `jig here` and nothing at the gate.
func TestKeepsConflictAdviceIsShownByGateAndPublishBeforeTheirWork(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	for _, id := range []string{"JIG-2", "JIG-3"} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fx.RepoDir, "shared.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, fx.RepoDir, "add", "-A")
	}
	write("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	gitIn(t, fx.RepoDir, "commit", "-m", "add shared.txt")
	branch := func(name, body string) {
		t.Helper()
		gitIn(t, fx.RepoDir, "switch", "-c", name, "main")
		write(body)
		gitIn(t, fx.RepoDir, "commit", "-m", "change on "+name)
	}
	branch("jig/JIG-1", "ONE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("jig/JIG-2", "TWO\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("jig/JIG-3", "1\n2\n3\n4\n5\n6\n7\n8\n9\nTHREE\n")

	const table = "conflict[1]{repo,ticket,file,branch}:\n  fixture-repo,JIG-2,shared.txt,jig/JIG-2\n"
	gitIn(t, fx.RepoDir, "switch", "jig/JIG-1")
	here, _ := runMain(t, e, "", "here")
	for _, want := range []string{
		"warning: the branch of JIG-1 cannot merge with the branch of another open ticket\n",
		table,
		"jig depend JIG-2 --on JIG-1 --kind stacked",
		"info: JIG-1 shares shared.txt with JIG-3; merges cleanly",
	} {
		if !strings.Contains(here, want) {
			t.Errorf("jig here lacks %q:\n%s", want, here)
		}
	}
	for _, verb := range []string{"next", "gate", "publish"} {
		out, _ := runMain(t, e, "", verb, "JIG-1")
		if !strings.Contains(out, table) {
			t.Errorf("jig %s JIG-1 lacks the conflict:\n%s", verb, out)
		}
	}
	gate, _ := runMain(t, e, "", "gate", "JIG-1")
	if i := strings.Index(gate, "warning: the branch of JIG-1"); i != 0 {
		t.Errorf("the conflict does not come first in the gate output (at %d):\n%s", i, gate)
	}

	gitIn(t, fx.RepoDir, "switch", "jig/JIG-3")
	clean, _ := runMain(t, e, "", "here")
	if strings.Contains(clean, "warning: the branch of") || strings.Contains(clean, "conflict[") {
		t.Errorf("a clean pair warned in jig here:\n%s", clean)
	}
	if !strings.Contains(clean, "info: JIG-3 shares shared.txt with JIG-1; merges cleanly") {
		t.Errorf("jig here lacks the clean line:\n%s", clean)
	}
	cleanGate, _ := runMain(t, e, "", "gate", "JIG-3")
	if strings.Contains(cleanGate, "conflict[") || strings.Contains(cleanGate, "merges cleanly") {
		t.Errorf("jig gate JIG-3 printed advice for a clean pair:\n%s", cleanGate)
	}
}
