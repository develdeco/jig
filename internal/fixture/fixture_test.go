package fixture

import (
	"errors"
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

// TestBuild proves Build materializes a usable fixture directly into a
// caller-chosen directory, with no *testing.T on the call path: every path
// on the returned Fixture is rooted under the dir the caller passed in, the
// store opens, its slices read back, and the scenario tree is present.
func TestBuild(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "built")

	fx, err := Build(dir, Opts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if fx.Dir != wantDir {
		t.Fatalf("fx.Dir = %q, want %q", fx.Dir, wantDir)
	}
	for _, p := range []string{fx.RepoDir, fx.RepoRemote, fx.StoreDir, fx.StoreRemote, fx.ScenarioDir} {
		if !strings.HasPrefix(p, wantDir+string(filepath.Separator)) {
			t.Fatalf("path %q is not rooted under dir %q", p, wantDir)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	if len(slices) != 4 {
		t.Fatalf("got %d slices, want 4", len(slices))
	}
}

// TestBuildRelativeDir proves Build resolves a relative dir to an absolute
// path before doing any work, so a git command it runs (whose working
// directory and whose path arguments must agree on the same root) never
// resolves a path twice against the caller's working directory.
func TestBuildRelativeDir(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	fx, err := Build("rel-out", Opts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !filepath.IsAbs(fx.Dir) {
		t.Fatalf("fx.Dir = %q, want an absolute path", fx.Dir)
	}
	for _, p := range []string{fx.RepoDir, fx.RepoRemote, fx.StoreDir, fx.StoreRemote, fx.ScenarioDir} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
	}
}

// TestBuildEnvtoolStaysUnderDir proves Build copies the envtool helper into
// dir/bin and points the fixture's jig.yaml there, instead of leaving it
// referencing the process-lifetime cache dir outside the caller's dir.
func TestBuildEnvtoolStaysUnderDir(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "built")

	fx, err := Build(dir, Opts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	envtoolBin := filepath.Join(wantDir, "bin", "envtool"+exeSuffix())
	if _, err := os.Stat(envtoolBin); err != nil {
		t.Fatalf("stat %s: %v", envtoolBin, err)
	}

	data, err := os.ReadFile(filepath.Join(fx.RepoDir, ".claude", "jig.yaml"))
	if err != nil {
		t.Fatalf("read jig.yaml: %v", err)
	}
	if !strings.Contains(string(data), filepath.ToSlash(envtoolBin)) {
		t.Fatalf("jig.yaml does not reference %s:\n%s", envtoolBin, data)
	}
}

// TestBuildIgnoresHostGitConfig proves Build's own git calls do not inherit
// the host's system or global git config: forced commit signing with no
// usable signer would otherwise fail every commit Build makes, and a config
// file git cannot parse would fail every git call (init, config, clone,
// remote add and commit).
func TestBuildIgnoresHostGitConfig(t *testing.T) {
	t.Run("global signing config", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		body := "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = false\n"
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatalf("write host gitconfig: %v", err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)

		dir := filepath.Join(t.TempDir(), "built")
		if _, err := Build(dir, Opts{}); err != nil {
			t.Fatalf("Build: %v", err)
		}
	})

	// A malformed global config, unlike a well-formed but signing one, fails
	// every git call that reads it, not only commit: this arm pins
	// hermeticGitEnv being passed to every runGit call, not only commitAll.
	t.Run("malformed global config", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(cfg, []byte("[broken\n"), 0o644); err != nil {
			t.Fatalf("write malformed host gitconfig: %v", err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)

		dir := filepath.Join(t.TempDir(), "built")
		if _, err := Build(dir, Opts{}); err != nil {
			t.Fatalf("Build: %v", err)
		}
	})

	// gittest.Run's TestMain sets GIT_CONFIG_NOSYSTEM=1 for the whole test
	// process, so it alone cannot pin hermeticGitEnv's own
	// "GIT_CONFIG_NOSYSTEM=1": no test can observe that line being dropped.
	// Clearing it here (t.Setenv registers the process-wide value's
	// restoration; os.Unsetenv then actually removes it for this arm) makes
	// the system config below matter only if hermeticGitEnv sets
	// GIT_CONFIG_NOSYSTEM itself.
	t.Run("system signing config", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		body := "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = false\n"
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatalf("write host system gitconfig: %v", err)
		}
		t.Setenv("GIT_CONFIG_NOSYSTEM", "")
		os.Unsetenv("GIT_CONFIG_NOSYSTEM")
		t.Setenv("GIT_CONFIG_SYSTEM", cfg)

		dir := filepath.Join(t.TempDir(), "built")
		if _, err := Build(dir, Opts{}); err != nil {
			t.Fatalf("Build: %v", err)
		}
	})
}

// TestBuildRefusesNonEmptyDir proves Build refuses to materialize into a
// directory that already has something in it, rather than silently mixing
// its output with whatever is there, and leaves the directory and the
// caller's JIG_HOME exactly as they were.
func TestBuildRefusesNonEmptyDir(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "already-here.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed non-empty dir: %v", err)
	}

	if _, err := Build(dir, Opts{}); err == nil {
		t.Fatal("expected Build to refuse a non-empty directory, got nil error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "already-here.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("dir contents = %v, want only [already-here.txt]", names)
	}
	if _, err := os.Stat(filepath.Join(jigHome, "projects.yaml")); !os.IsNotExist(err) {
		t.Fatalf("stat JIG_HOME/projects.yaml = %v, want IsNotExist", err)
	}
}

// TestBuildCleansUpAfterLateFailure proves Build's cleanup also fires when
// the failure is its very last step, the JIG_HOME mapping write
// (project.InitProject), after every other write under dir already
// succeeded. A JIG_HOME that is a regular file rather than a directory makes
// that write fail: os.MkdirAll(filepath.Dir(JIG_HOME/projects.yaml), ...)
// cannot create a directory where a file already exists.
func TestBuildCleansUpAfterLateFailure(t *testing.T) {
	badJigHome := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badJigHome, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed JIG_HOME file: %v", err)
	}

	t.Run("dir Build creates", func(t *testing.T) {
		t.Setenv("JIG_HOME", badJigHome)
		dir := filepath.Join(t.TempDir(), "built")

		if _, err := Build(dir, Opts{}); err == nil {
			t.Fatal("expected Build to fail with an unusable JIG_HOME, got nil error")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("stat %s = %v, want IsNotExist (Build created this dir, so cleanup must remove it entirely)", dir, err)
		}
	})

	t.Run("pre-existing empty dir", func(t *testing.T) {
		t.Setenv("JIG_HOME", badJigHome)
		dir := t.TempDir()

		if _, err := Build(dir, Opts{}); err == nil {
			t.Fatal("expected Build to fail with an unusable JIG_HOME, got nil error")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("dir %s left non-empty after a failed Build: %v (dir pre-existed, so cleanup must empty it, not remove it)", dir, entries)
		}
	})
}

// TestBuildUnknownScenarioBranch proves Build returns an error naming the
// branch, rather than silently ignoring it, when ScenarioBranch names an
// overlay that does not exist under testdata/fixture/scenario-branches, and
// that dir is left empty or absent afterwards, with no JIG_HOME mapping
// registered for the fixture it failed to build.
func TestBuildUnknownScenarioBranch(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	dir := filepath.Join(t.TempDir(), "built")

	_, err := Build(dir, Opts{ScenarioBranch: "does-not-exist"})
	if err == nil {
		t.Fatal("expected Build to error on an unknown scenario branch, got nil")
	}
	if !errors.Is(err, ErrUnknownScenarioBranch) {
		t.Fatalf("error = %v, want errors.Is(err, ErrUnknownScenarioBranch)", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error = %q, want it to name the branch %q", err, "does-not-exist")
	}

	if entries, statErr := os.ReadDir(dir); statErr == nil {
		if len(entries) != 0 {
			t.Fatalf("dir %s left non-empty after a failed Build: %v", dir, entries)
		}
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("stat %s: %v", dir, statErr)
	}
	if _, err := os.Stat(filepath.Join(jigHome, "projects.yaml")); !os.IsNotExist(err) {
		t.Fatalf("stat JIG_HOME/projects.yaml = %v, want IsNotExist", err)
	}
}
