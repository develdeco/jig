package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/store"
)

func runGitInProject(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadTrackerScalar(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: github
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker != "github" {
		t.Errorf("Tracker = %q, want github", cfg.Tracker)
	}
	if cfg.TrackerCmd != "" {
		t.Errorf("TrackerCmd = %q, want empty", cfg.TrackerCmd)
	}
	if cfg.Name != "demo" || cfg.SchemaVersion != 1 || cfg.TicketFormat != "JIG-{n}" {
		t.Errorf("cfg = %+v, unexpected", cfg)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Remote != "https://example.invalid/org/demo.git" {
		t.Errorf("Repos = %+v, unexpected", cfg.Repos)
	}
}

// TestLoadRoutes checks that a project.yaml's routes: map round-trips into
// Config.Routes, and that a project.yaml with no routes: key leaves it nil
// (the "use the spec's defaults" case, resolved by the caller).
func TestLoadRoutes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
routes:
  pr.description:
    - changelog/consolidated.md
  pr.comments:
    - changelog/consolidated.md
  ticket.comments:
    - changelog/consolidated.md
    - gate/round-*/diff-changelog.md
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Routes["pr.description"]; len(got) != 1 || got[0] != "changelog/consolidated.md" {
		t.Errorf(`Routes["pr.description"] = %v, unexpected`, got)
	}
	if got := cfg.Routes["ticket.comments"]; len(got) != 2 {
		t.Errorf(`Routes["ticket.comments"] = %v, want 2 entries`, got)
	}

	noRoutes := filepath.Join(dir, "no-routes.yaml")
	writeFile(t, noRoutes, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`)
	cfg2, err := Load(noRoutes)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg2.Routes != nil {
		t.Errorf("Routes = %v, want nil when routes: is absent", cfg2.Routes)
	}
}

func TestLoadTrackerCommandMap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker:
  command: ./scripts/tracker.sh
repos: []
platform: platform/
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker != "command" {
		t.Errorf("Tracker = %q, want command", cfg.Tracker)
	}
	if cfg.TrackerCmd != "./scripts/tracker.sh" {
		t.Errorf("TrackerCmd = %q, want ./scripts/tracker.sh", cfg.TrackerCmd)
	}
}

func TestRepoName(t *testing.T) {
	cases := []struct{ remote, want string }{
		{"https://example.invalid/org/demo.git", "demo"},
		{"https://example.invalid/org/demo", "demo"},
		{`C:\repos\demo`, "demo"},
		{"/home/user/demo.git", "demo"},
	}
	for _, c := range cases {
		r := Repo{Remote: c.remote}
		if got := r.Name(); got != c.want {
			t.Errorf("Repo{%q}.Name() = %q, want %q", c.remote, got, c.want)
		}
	}
}

func TestMintLocalID(t *testing.T) {
	cfg := Config{TicketFormat: "JIG-{n}"}
	if got := cfg.MintLocalID(7); got != "JIG-7" {
		t.Errorf("MintLocalID(7) = %q, want JIG-7", got)
	}
}

func TestInitStandalone(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	storePath, err := InitStandalone(repoDir)
	if err != nil {
		t.Fatalf("InitStandalone: %v", err)
	}

	wantStore := filepath.Join(parent, "myrepo-tickets")
	absWant, _ := filepath.Abs(wantStore)
	absGot, _ := filepath.Abs(storePath)
	if absGot != absWant {
		t.Fatalf("storePath = %q, want %q", absGot, absWant)
	}

	if fi, err := os.Stat(filepath.Join(storePath, ".git")); err != nil || !fi.IsDir() {
		t.Errorf(".git missing under store: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(storePath, "platform")); err != nil || !fi.IsDir() {
		t.Errorf("platform/ missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storePath, "ledger.md")); err != nil {
		t.Errorf("ledger.md missing: %v", err)
	}

	cfg, err := Load(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		t.Fatalf("Load generated project.yaml: %v", err)
	}
	if cfg.SchemaVersion != 1 || cfg.Name != "myrepo" || cfg.TicketFormat != "T-{n}" || cfg.Tracker != "local" || cfg.Platform != "platform/" {
		t.Errorf("cfg = %+v, unexpected", cfg)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("Repos = %+v, want 1 entry", cfg.Repos)
	}
	absRepo, _ := filepath.Abs(repoDir)
	if cfg.Repos[0].Remote != absRepo {
		t.Errorf("Repos[0].Remote = %q, want %q", cfg.Repos[0].Remote, absRepo)
	}
}

// TestInitStandaloneGitignoreKeepsLockFilesUntracked asserts InitStandalone
// writes a store-root .gitignore covering store.Lock's sidecar "*.lock"
// files and store.AtomicWrite's ".*.tmp" scratch files, and that a real
// locked write (store.WriteSliceState) never shows up in `git status`.
func TestInitStandaloneGitignoreKeepsLockFilesUntracked(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	storePath, err := InitStandalone(repoDir)
	if err != nil {
		t.Fatalf("InitStandalone: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(storePath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "*.lock") || !strings.Contains(string(data), ".*.tmp") {
		t.Fatalf(".gitignore = %q, want it to ignore *.lock and .*.tmp", data)
	}

	runGitInProject(t, storePath, "config", "user.name", "tester")
	runGitInProject(t, storePath, "config", "user.email", "tester@example.invalid")
	runGitInProject(t, storePath, "add", "-A")
	runGitInProject(t, storePath, "commit", "-m", "init")

	st := &store.Store{Root: storePath}
	if err := st.WriteSliceState("T-1", "a", store.SliceState{State: "queued"}); err != nil {
		t.Fatalf("WriteSliceState: %v", err)
	}

	statusOut := runGitInProject(t, storePath, "status", "--porcelain")
	for _, line := range strings.Split(statusOut, "\n") {
		if strings.Contains(line, ".lock") {
			t.Fatalf("git status shows a lock file (should be gitignored): %q\nfull status:\n%s", line, statusOut)
		}
	}
}

func TestMachineMappingRoundTrip(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	in := map[string]MachineProject{
		"demo": {Store: `C:\stores\demo`, Clones: map[string]string{"demo": `C:\repos\demo`}},
	}
	if err := SaveMachine(in); err != nil {
		t.Fatalf("SaveMachine: %v", err)
	}
	out, err := LoadMachine()
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestLoadMachineMissingFileReturnsEmpty(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	m, err := LoadMachine()
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("LoadMachine = %+v, want empty", m)
	}
}

func TestInitProject(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "project.yaml"), `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
`)
	cloneDir := t.TempDir()

	cfg, err := InitProject(storeDir, map[string]string{"demo": cloneDir})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if cfg.Name != "demo" {
		t.Fatalf("cfg.Name = %q, want demo", cfg.Name)
	}

	machine, err := LoadMachine()
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	mp, ok := machine["demo"]
	if !ok {
		t.Fatalf("machine mapping missing demo: %+v", machine)
	}
	absStore, _ := filepath.Abs(storeDir)
	absClone, _ := filepath.Abs(cloneDir)
	if mp.Store != absStore {
		t.Errorf("mp.Store = %q, want %q", mp.Store, absStore)
	}
	if mp.Clones["demo"] != absClone {
		t.Errorf("mp.Clones[demo] = %q, want %q", mp.Clones["demo"], absClone)
	}
}

func TestInitProjectRejectsUnknownClone(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, "project.yaml"), `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
`)
	_, err := InitProject(storeDir, map[string]string{"other": t.TempDir()})
	if err == nil {
		t.Fatal("expected error for unknown clone name")
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	// 1. explicit --store flag wins over everything.
	explicitStore := t.TempDir()
	writeFile(t, filepath.Join(explicitStore, "project.yaml"), `
schema_version: 1
name: explicit
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`)
	cwdWithOwnStore := t.TempDir()
	writeFile(t, filepath.Join(cwdWithOwnStore, "project.yaml"), `
schema_version: 1
name: cwdstore
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`)
	storePath, cfg, err := Resolve(cwdWithOwnStore, explicitStore)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if storePath != explicitStore || cfg.Name != "explicit" {
		t.Fatalf("Resolve with --store = (%q, %q), want explicit store to win", storePath, cfg.Name)
	}

	// 2. cwd store (project.yaml in cwd) wins over machine mapping, when no
	// --store flag is given.
	storePath, cfg, err = Resolve(cwdWithOwnStore, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if storePath != cwdWithOwnStore || cfg.Name != "cwdstore" {
		t.Fatalf("Resolve cwd store = (%q, %q), want cwdstore", storePath, cfg.Name)
	}

	// 3. cwd inside a machine-mapped clone, no local project.yaml.
	mappedStore := t.TempDir()
	writeFile(t, filepath.Join(mappedStore, "project.yaml"), `
schema_version: 1
name: mapped
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`)
	cloneRoot := t.TempDir()
	subdir := filepath.Join(cloneRoot, "sub", "dir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	machine := map[string]MachineProject{
		"mapped": {Store: mappedStore, Clones: map[string]string{"mapped": cloneRoot}},
	}
	if err := SaveMachine(machine); err != nil {
		t.Fatalf("SaveMachine: %v", err)
	}
	storePath, cfg, err = Resolve(subdir, "")
	if err != nil {
		t.Fatalf("Resolve via machine mapping: %v", err)
	}
	if storePath != mappedStore || cfg.Name != "mapped" {
		t.Fatalf("Resolve via machine mapping = (%q, %q), want mapped store", storePath, cfg.Name)
	}

	// 4. nothing matches → error.
	orphan := t.TempDir()
	if _, _, err := Resolve(orphan, ""); err == nil {
		t.Fatal("expected error when no store can be resolved")
	}
}
