package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestConflictAdviceIsARealConflictShownWhereTheSessionLooks: two tickets whose
// branches change the same lines of one file are told of each other, with the
// file, by `jig here` and `jig next <T>`, and `jig gate` prints the same block
// before it does anything else; a
// third ticket that changes a different part of the same file merges cleanly,
// gets a quiet info line in `jig here` and nothing in the gate.
func TestConflictAdviceIsARealConflictShownWhereTheSessionLooks(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
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
		gitLog(t, fx.RepoDir, "add", "-A")
	}
	write("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	commitPinned(t, fx.RepoDir, "add shared.txt")
	branch := func(name, body string) {
		t.Helper()
		gitLog(t, fx.RepoDir, "switch", "-c", name, "main")
		write(body)
		commitPinned(t, fx.RepoDir, "change on "+name)
	}
	branch("jig/JIG-1", "ONE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("jig/JIG-2", "TWO\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("jig/JIG-3", "1\n2\n3\n4\n5\n6\n7\n8\n9\nTHREE\n")

	const table = "conflict[1]{repo,ticket,file,branch}:\n"
	gitLog(t, fx.RepoDir, "switch", "jig/JIG-1")
	here := runJig(t, env, fx.RepoDir, "here")
	for _, want := range []string{
		"warning: the branch of JIG-1 cannot merge with the branch of another open ticket\n",
		table + "  fixture-repo,JIG-2,shared.txt,jig/JIG-2\n",
		"jig depend JIG-2 --on JIG-1 --kind stacked",
		"info: JIG-1 shares shared.txt with JIG-3; merges cleanly",
	} {
		if !strings.Contains(here.Stdout, want) {
			t.Errorf("jig here lacks %q:\n%s", want, here.Stdout)
		}
	}
	next := runJig(t, env, fx.RepoDir, "next", "JIG-1")
	if !strings.Contains(next.Stdout, table+"  fixture-repo,JIG-2,shared.txt,jig/JIG-2\n") {
		t.Errorf("jig next JIG-1 lacks the conflict:\n%s", next.Stdout)
	}

	gate := runJig(t, env, fx.RepoDir, "gate", "JIG-1")
	if !strings.Contains(gate.Stdout, table+"  fixture-repo,JIG-2,shared.txt,jig/JIG-2\n") {
		t.Errorf("jig gate JIG-1 lacks the conflict before its work:\n%s", gate.Stdout)
	}
	if i, j := strings.Index(gate.Stdout, "warning: the branch of JIG-1"), strings.Index(gate.Stdout, "gate"); i != 0 && j >= 0 && i > j {
		t.Errorf("the conflict does not come first in the gate output:\n%s", gate.Stdout)
	}

	// jig publish prints the same block before it pushes (and then refuses a
	// ticket with nothing to publish), and jig here --json carries the rows.
	publish := runJig(t, env, fx.RepoDir, "publish", "JIG-1")
	if !strings.Contains(publish.Stdout, table+"  fixture-repo,JIG-2,shared.txt,jig/JIG-2\n") {
		t.Errorf("jig publish JIG-1 lacks the conflict:\n%s", publish.Stdout)
	}
	hereJSON := runJig(t, env, fx.RepoDir, "here", "--json")
	if !strings.Contains(hereJSON.Stdout, `"conflicts":[{"repo":"fixture-repo","ticket":"JIG-2","file":"shared.txt","branch":"jig/JIG-2","first":"JIG-1"}]`) {
		t.Errorf("jig here --json lacks the conflicts array:\n%s", hereJSON.Stdout)
	}

	// The clean pair: nothing for JIG-3 at the gate, no warning in jig here.
	gitLog(t, fx.RepoDir, "switch", "jig/JIG-3")
	clean := runJig(t, env, fx.RepoDir, "here")
	if strings.Contains(clean.Stdout, "warning: the branch of") || strings.Contains(clean.Stdout, "conflict[") {
		t.Errorf("a clean pair warned in jig here:\n%s", clean.Stdout)
	}
	if !strings.Contains(clean.Stdout, "info: JIG-3 shares shared.txt with JIG-1; merges cleanly") {
		t.Errorf("jig here lacks the clean line:\n%s", clean.Stdout)
	}
	cleanGate := runJig(t, env, fx.RepoDir, "gate", "JIG-3")
	if strings.Contains(cleanGate.Stdout, "conflict[") || strings.Contains(cleanGate.Stdout, "merges cleanly") {
		t.Errorf("jig gate JIG-3 printed advice for a clean pair:\n%s", cleanGate.Stdout)
	}
}
