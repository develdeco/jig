package fixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

func TestGenerate(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := Generate(t, Opts{})

	// alpha's TestClamp fails as committed (the red-first repro).
	cmd := exec.Command(goBinaryPath(), "test", "./alpha/...")
	cmd.Dir = fx.RepoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected `go test ./alpha/...` to fail as committed, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "TestClamp") {
		t.Fatalf("expected a TestClamp failure, got:\n%s", out)
	}

	// beta passes as committed.
	cmd = exec.Command(goBinaryPath(), "test", "./beta/...")
	cmd.Dir = fx.RepoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected `go test ./beta/...` to pass, got:\n%s", out)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	brief, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "brief.md"))
	if err != nil {
		t.Fatalf("read brief.md: %v", err)
	}
	wantHashes := store.BriefSectionHashes(brief)
	if len(wantHashes) != 5 {
		t.Fatalf("got %d brief sections, want 5 (Goal + slices A-D)", len(wantHashes))
	}

	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	if len(slices) != 4 {
		t.Fatalf("got %d slices, want 4", len(slices))
	}
	for _, sl := range slices {
		if len(sl.FromBrief) == 0 {
			t.Fatalf("slice %s: no from_brief hashes", sl.ID)
		}
		for _, h := range sl.FromBrief {
			if strings.Contains(h, "@HASH") {
				t.Fatalf("slice %s: unresolved placeholder %q", sl.ID, h)
			}
			matched := false
			for _, want := range wantHashes {
				if h == want {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("slice %s: hash %q does not match any brief section hash", sl.ID, h)
			}
		}
	}

	if !st.HasRemote() {
		t.Fatal("expected the store to have a remote")
	}
	if _, err := os.Stat(fx.RepoRemote); err != nil {
		t.Fatalf("repo remote missing: %v", err)
	}
	if fx.StoreRemote == "" {
		t.Fatal("expected a store remote (Standalone was not set)")
	}
	if _, err := os.Stat(fx.StoreRemote); err != nil {
		t.Fatalf("store remote missing: %v", err)
	}
}

// TestGenerateStoreGitignoreKeepsLockFilesUntracked asserts the fixture
// store carries the same "*.lock"/".*.tmp" .gitignore as
// project.InitStandalone, and that a real locked write never shows up in
// `git status` for the (already-committed) fixture store.
func TestGenerateStoreGitignoreKeepsLockFilesUntracked(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := Generate(t, Opts{})

	data, err := os.ReadFile(filepath.Join(fx.StoreDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "*.lock") || !strings.Contains(string(data), ".*.tmp") {
		t.Fatalf(".gitignore = %q, want it to ignore *.lock and .*.tmp", data)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green"}); err != nil {
		t.Fatalf("WriteSliceState: %v", err)
	}

	out, err := gitx.Run(fx.StoreDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, ".lock") {
			t.Fatalf("git status shows a lock file (should be gitignored): %q\nfull status:\n%s", line, out)
		}
	}
}

func TestPatchSequence(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := Generate(t, Opts{})

	steps := []struct{ slice, attempt string }{
		{"a", "1"},
		{"b", "1"},
		{"b", "2"},
		{"c", "2"},
		{"d", "1"},
		{"fix-1", "1"},
	}
	for _, step := range steps {
		patch := filepath.Join(fx.ScenarioDir, "slices", step.slice, "attempt-"+step.attempt, "patch.diff")

		if _, err := gitx.Run(fx.RepoDir, "apply", patch); err != nil {
			t.Fatalf("git apply %s: %v", patch, err)
		}
		if _, err := gitx.Run(fx.RepoDir, "add", "-A"); err != nil {
			t.Fatalf("git add after %s: %v", patch, err)
		}
		if _, err := gitx.RunEnv(fx.RepoDir, identityEnv, "commit", "-m", step.slice+" attempt-"+step.attempt); err != nil {
			t.Fatalf("git commit after %s: %v", patch, err)
		}
	}

	cmd := exec.Command(goBinaryPath(), "test", "./...")
	cmd.Dir = fx.RepoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected the fixture repo to end green after the scenario sequence, got:\n%s", out)
	}
}

func TestGenerateStandalone(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := Generate(t, Opts{Standalone: true})

	if fx.StoreRemote != "" {
		t.Fatalf("expected no store remote, got %q", fx.StoreRemote)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if st.HasRemote() {
		t.Fatal("expected a standalone store to have no remote")
	}
}

func TestGenerateBranch(t *testing.T) {
	cases := []struct {
		name            string
		branch          string
		attempt1Outcome string
		attempt2Exists  bool
		attempt2Outcome string
	}{
		{
			name:            "oracle-wrong",
			branch:          "oracle-wrong",
			attempt1Outcome: "oracle-wrong",
			attempt2Exists:  true,
			attempt2Outcome: "green",
		},
		{
			name:            "stall",
			branch:          "stall",
			attempt1Outcome: "code-bug",
			attempt2Exists:  true,
			attempt2Outcome: "code-bug",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JIG_HOME", t.TempDir())
			fx := Generate(t, Opts{ScenarioBranch: tc.branch})

			attempt1 := filepath.Join(fx.ScenarioDir, "slices", "a", "attempt-1")
			result, err := os.ReadFile(filepath.Join(attempt1, "result.json"))
			if err != nil {
				t.Fatalf("read attempt-1 result.json: %v", err)
			}
			if !strings.Contains(string(result), `"outcome": "`+tc.attempt1Outcome+`"`) {
				t.Fatalf("attempt-1 result.json = %s, want outcome %q", result, tc.attempt1Outcome)
			}

			patch, err := os.ReadFile(filepath.Join(attempt1, "patch.diff"))
			if err != nil {
				t.Fatalf("read attempt-1 patch.diff: %v", err)
			}
			if strings.TrimSpace(string(patch)) != "" {
				t.Fatalf("expected attempt-1 patch.diff to be empty for branch %s, got %q", tc.branch, patch)
			}

			if tc.attempt2Exists {
				attempt2Result, err := os.ReadFile(filepath.Join(fx.ScenarioDir, "slices", "a", "attempt-2", "result.json"))
				if err != nil {
					t.Fatalf("read attempt-2 result.json: %v", err)
				}
				if !strings.Contains(string(attempt2Result), `"outcome": "`+tc.attempt2Outcome+`"`) {
					t.Fatalf("attempt-2 result.json = %s, want outcome %q", attempt2Result, tc.attempt2Outcome)
				}
			}
		})
	}
}
