package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestKeepsNextWarnsOfOverlappingBranches: local branches of two tickets that
// change the same file since main are named by `jig next`; a branch that binds
// no ticket, and a file only one ticket changes, are not.
func TestKeepsNextWarnsOfOverlappingBranches(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	branchChanging := func(branch string, files map[string]string) {
		t.Helper()
		gitIn(t, fx.RepoDir, "switch", "-c", branch, "main")
		for rel, content := range files {
			if err := os.WriteFile(filepath.Join(fx.RepoDir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gitIn(t, fx.RepoDir, "add", "-A")
		gitIn(t, fx.RepoDir, "commit", "-m", "change on "+branch)
		gitIn(t, fx.RepoDir, "switch", "main")
	}
	branchChanging("jig/JIG-1", map[string]string{"alpha/alpha.go": "package alpha\n\n// one\n", "beta/one.go": "package beta\n"})
	branchChanging("jig/JIG-2", map[string]string{"alpha/alpha.go": "package alpha\n\n// two\n", "beta/two.go": "package beta\n"})
	branchChanging("scratch", map[string]string{"alpha/alpha.go": "package alpha\n\n// scratch\n"})

	out, code := runMain(t, e, "", "next")
	if code != 0 {
		t.Fatalf("jig next: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"warning: branches of different tickets change the same files\n",
		"overlap[1]{repo,file,tickets,conflict}:\n  fixture-repo,alpha/alpha.go,JIG-1+JIG-2,conflict\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next lacks %q:\n%s", want, out)
		}
	}
}
