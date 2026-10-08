package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

func runGitInProject(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v (in %s): %v", args, dir, err)
	}
	return out
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

// wantValidationError fails unless err is a *axi.Error with code
// VALIDATION_ERROR, the code every project.yaml load refusal this file
// covers uses.
func wantValidationError(t *testing.T, err error) {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("err = %v, want *axi.Error VALIDATION_ERROR", err)
	}
}

// TestLoadTrackerLocalMeansNoMirrors checks that the legacy tracker: local
// still loads (until L3's migration rewrites project.yaml), the one old
// tracker: value trackers: replacing tracker: leaves standing.
func TestLoadTrackerLocalMeansNoMirrors(t *testing.T) {
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
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "demo" || cfg.SchemaVersion != 1 || cfg.TicketFormat != "JIG-{n}" {
		t.Errorf("cfg = %+v, unexpected", cfg)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Remote != "https://example.invalid/org/demo.git" {
		t.Errorf("Repos = %+v, unexpected", cfg.Repos)
	}
}

// TestLoadAbsentTrackersMeansNoMirrors checks that a project.yaml with
// neither tracker: nor trackers: loads fine, same as an explicit empty
// trackers: list.
func TestLoadAbsentTrackersMeansNoMirrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
repos: []
platform: platform/
`)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// TestLoadTrackersEmptyMeansNoMirrors checks that an explicit trackers: []
// loads fine, same as an absent trackers: key.
func TestLoadTrackersEmptyMeansNoMirrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers: []
repos: []
platform: platform/
`)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// TestLoadRefusesEveryOtherTrackerValue checks that every tracker: value
// besides "local" - github, jira, linear, and the old {command: ...} map -
// is refused when the config loads.
func TestLoadRefusesEveryOtherTrackerValue(t *testing.T) {
	cases := []struct {
		name, trackerYAML string
	}{
		{"github", "tracker: github"},
		{"jira", "tracker: jira"},
		{"linear", "tracker: linear"},
		{"command map", "tracker:\n  command: ./scripts/tracker.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "project.yaml")
			writeFile(t, p, fmt.Sprintf(`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
%s
repos: []
platform: platform/
`, c.trackerYAML))
			_, err := Load(p)
			wantValidationError(t, err)
		})
	}
}

// TestLoadRefusesATrackersEntry checks that a trackers: entry under an
// unsupported key (only github: is supported) is refused when the config
// loads.
func TestLoadRefusesATrackersEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers:
  - type: github
repos: []
platform: platform/
`)
	_, err := Load(p)
	wantValidationError(t, err)
}

// TestLoadRefusesAMalformedTrackersShape checks that a trackers: value that
// is not a sequence - a scalar (the one-character slip from tracker:) or a
// mapping - is refused as a VALIDATION_ERROR, the same as a well-formed but
// unsupported entry, rather than surfacing yaml.v3's own decode error.
func TestLoadRefusesAMalformedTrackersShape(t *testing.T) {
	cases := []struct {
		name, trackersYAML string
	}{
		{"scalar", "trackers: github"},
		{"mapping", "trackers:\n  github: {}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "project.yaml")
			writeFile(t, p, fmt.Sprintf(`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
%s
repos: []
platform: platform/
`, c.trackersYAML))
			_, err := Load(p)
			wantValidationError(t, err)
		})
	}
}

// TestLoadRefusesTrackerAndTrackersTogether checks that tracker: and
// trackers: together are refused, even when tracker: carries its one
// remaining legal value.
func TestLoadRefusesTrackerAndTrackersTogether(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
trackers: []
repos: []
platform: platform/
`)
	_, err := Load(p)
	wantValidationError(t, err)
}

// TestLoadRefusesRoutes checks that a project.yaml declaring routes: -
// which only ever fed publish's now-gone route step - is refused when the
// config loads.
func TestLoadRefusesRoutes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
repos: []
platform: platform/
routes:
  pr.description:
    - changelog/consolidated.md
`)
	_, err := Load(p)
	wantValidationError(t, err)
}

// TestLoadAcceptsAGitHubTrackersEntry checks that a well-formed trackers:
// github: entry loads, populating Config.GitHub.
func TestLoadAcceptsAGitHubTrackersEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
repos: []
platform: platform/
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitHub == nil {
		t.Fatal("cfg.GitHub = nil, want a github entry")
	}
	if cfg.GitHub.Repo != "example/tracking" || cfg.GitHub.Project != "https://github.com/users/example/projects/7" {
		t.Errorf("cfg.GitHub = %+v, unexpected", cfg.GitHub)
	}
}

// TestLoadRefusesASecondTrackersEntry checks that two entries under
// trackers: are refused, even when the first is a well-formed github:.
func TestLoadRefusesASecondTrackersEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
  - github:
      repo: example/other
      project: https://github.com/users/example/projects/8
repos: []
platform: platform/
`)
	_, err := Load(p)
	wantValidationError(t, err)
}

// TestLoadRefusesAGitHubEntryMissingAField checks that a github: entry with
// only one of repo:/project: is refused, each required on its own.
func TestLoadRefusesAGitHubEntryMissingAField(t *testing.T) {
	cases := []struct {
		name, entryYAML string
	}{
		{"no project", "repo: example/tracking"},
		{"no repo", "project: https://github.com/users/example/projects/7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "project.yaml")
			writeFile(t, p, fmt.Sprintf(`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers:
  - github:
      %s
repos: []
platform: platform/
`, c.entryYAML))
			_, err := Load(p)
			wantValidationError(t, err)
		})
	}
}

// TestLoadRefusesAGitHubEntryWithAnUnknownKey checks that a github: entry
// carrying a key besides repo:/project: is refused.
func TestLoadRefusesAGitHubEntryWithAnUnknownKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
      token: abc
repos: []
platform: platform/
`)
	_, err := Load(p)
	wantValidationError(t, err)
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

// TestRepoTargetBranch covers the one place the default target lives: a
// configured target wins, an empty one is "main".
func TestRepoTargetBranch(t *testing.T) {
	cases := []struct{ target, want string }{
		{"", "main"},
		{"develop", "develop"},
		{"main", "main"},
	}
	for _, c := range cases {
		r := Repo{Remote: "https://example.invalid/org/demo.git", Target: c.target}
		if got := r.TargetBranch(); got != c.want {
			t.Errorf("Repo{Target: %q}.TargetBranch() = %q, want %q", c.target, got, c.want)
		}
	}
}

func TestMintLocalID(t *testing.T) {
	cfg := Config{TicketFormat: "JIG-{n}"}
	if got := cfg.MintLocalID(7); got != "JIG-7" {
		t.Errorf("MintLocalID(7) = %q, want JIG-7", got)
	}
}

// TestStandaloneStoreDir checks that it returns the same path InitStandalone
// itself creates the store at, so a caller can check for an existing store
// before calling InitStandalone (which always overwrites one).
func TestStandaloneStoreDir(t *testing.T) {
	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")

	got, err := StandaloneStoreDir(repoDir)
	if err != nil {
		t.Fatalf("StandaloneStoreDir: %v", err)
	}
	want := filepath.Join(parent, "myrepo-tickets")
	if got != want {
		t.Fatalf("StandaloneStoreDir(%q) = %q, want %q", repoDir, got, want)
	}
}

func TestInitStandalone(t *testing.T) {
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
	if cfg.SchemaVersion != 1 || cfg.Name != "myrepo" || cfg.TicketFormat != "T-{n}" || cfg.Platform != "platform/" {
		t.Errorf("cfg = %+v, unexpected", cfg)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("Repos = %+v, want 1 entry", cfg.Repos)
	}

	data, err := os.ReadFile(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		t.Fatalf("read project.yaml: %v", err)
	}
	if !strings.Contains(string(data), "trackers: []") {
		t.Errorf("project.yaml = %s, want trackers: []", data)
	}
	if strings.Contains(string(data), "tracker:") {
		t.Errorf("project.yaml = %s, want no tracker: key", data)
	}
	absRepo, _ := filepath.Abs(repoDir)
	if cfg.Repos[0].Remote != absRepo {
		t.Errorf("Repos[0].Remote = %q, want %q", cfg.Repos[0].Remote, absRepo)
	}
}

// TestInitStandaloneReinitLeavesOtherDirtyFilesAlone checks InitStandalone's
// own re-init contract: run again against a store it already scaffolded, it
// resets project.yaml and ledger.md without error, even though the reset
// changes nothing (so there is nothing to commit), and it never sweeps an
// unrelated dirty file in the store into its own commit.
func TestInitStandaloneReinitLeavesOtherDirtyFilesAlone(t *testing.T) {
	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	storePath, err := InitStandalone(repoDir)
	if err != nil {
		t.Fatalf("InitStandalone: %v", err)
	}

	writeFile(t, filepath.Join(storePath, "unrelated.txt"), "dirty\n")

	if _, err := InitStandalone(repoDir); err != nil {
		t.Fatalf("InitStandalone (re-init): %v", err)
	}

	status := runGitInProject(t, storePath, "status", "--porcelain", "unrelated.txt")
	if !strings.Contains(status, "?? unrelated.txt") {
		t.Fatalf("status unrelated.txt = %q, want it still untracked", status)
	}
}

// TestInitStandaloneGitignoreKeepsLockFilesUntracked asserts InitStandalone
// writes a store-root .gitignore covering store.Lock's sidecar "*.lock"
// files and store.AtomicWrite's ".*.tmp" scratch files, and that a real
// locked write (store.WriteSliceState) never shows up in `git status`.
func TestInitStandaloneGitignoreKeepsLockFilesUntracked(t *testing.T) {
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
	jigHome := t.TempDir()

	in := map[string]MachineProject{
		"demo": {Store: `C:\stores\demo`, Clones: map[string]string{"demo": `C:\repos\demo`}},
	}
	if err := SaveMachine(jigHome, in); err != nil {
		t.Fatalf("SaveMachine: %v", err)
	}
	out, err := LoadMachine(jigHome)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestLoadMachineMissingFileReturnsEmpty(t *testing.T) {
	jigHome := t.TempDir()
	m, err := LoadMachine(jigHome)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("LoadMachine = %+v, want empty", m)
	}
}

func TestInitProject(t *testing.T) {
	jigHome := t.TempDir()

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

	cfg, err := InitProject(jigHome, storeDir, map[string]string{"demo": cloneDir})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if cfg.Name != "demo" {
		t.Fatalf("cfg.Name = %q, want demo", cfg.Name)
	}

	machine, err := LoadMachine(jigHome)
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
	jigHome := t.TempDir()

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
	_, err := InitProject(jigHome, storeDir, map[string]string{"other": t.TempDir()})
	if err == nil {
		t.Fatal("expected error for unknown clone name")
	}
}

func TestResolvePrecedence(t *testing.T) {
	jigHome := t.TempDir()

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
	storePath, cfg, err := Resolve(jigHome, cwdWithOwnStore, explicitStore)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if storePath != explicitStore || cfg.Name != "explicit" {
		t.Fatalf("Resolve with --store = (%q, %q), want explicit store to win", storePath, cfg.Name)
	}

	// 2. cwd store (project.yaml in cwd) wins over machine mapping, when no
	// --store flag is given.
	storePath, cfg, err = Resolve(jigHome, cwdWithOwnStore, "")
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
	if err := SaveMachine(jigHome, machine); err != nil {
		t.Fatalf("SaveMachine: %v", err)
	}
	storePath, cfg, err = Resolve(jigHome, subdir, "")
	if err != nil {
		t.Fatalf("Resolve via machine mapping: %v", err)
	}
	if storePath != mappedStore || cfg.Name != "mapped" {
		t.Fatalf("Resolve via machine mapping = (%q, %q), want mapped store", storePath, cfg.Name)
	}

	// 4. nothing matches → error.
	orphan := t.TempDir()
	if _, _, err := Resolve(jigHome, orphan, ""); err == nil {
		t.Fatal("expected error when no store can be resolved")
	}
}

// TestResolveFallsBackToSiblingStandaloneStore checks that Resolve finds a
// repo's own "jig init --standalone" store even when cwd is the repo dir
// itself, not the store: without this, the worked example in `jig`'s own
// bare-usage help (init --standalone, then jig ticket new, run, solve, all
// run from the repo) fails on its very first command.
func TestResolveFallsBackToSiblingStandaloneStore(t *testing.T) {
	jigHome := t.TempDir()

	parent := t.TempDir()
	repoDir := filepath.Join(parent, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	storePath, err := InitStandalone(repoDir)
	if err != nil {
		t.Fatalf("InitStandalone: %v", err)
	}

	storeDir, cfg, err := Resolve(jigHome, repoDir, "")
	if err != nil {
		t.Fatalf("Resolve(repoDir, \"\"): %v", err)
	}
	absStore, _ := filepath.Abs(storePath)
	absGot, _ := filepath.Abs(storeDir)
	if absGot != absStore {
		t.Fatalf("storeDir = %q, want %q", absGot, absStore)
	}
	if cfg.Name != "myrepo" {
		t.Fatalf("cfg.Name = %q, want %q", cfg.Name, "myrepo")
	}

	// A cwd with neither its own project.yaml, a machine mapping, nor a
	// sibling standalone store still refuses, same as before.
	if _, _, err := Resolve(jigHome, t.TempDir(), ""); err == nil {
		t.Fatal("expected error when no store can be resolved")
	}
}

// TestMachineMappingRefusesNoJigHome: an empty jig home would read or write
// projects.yaml in the working directory, so both are refused.
func TestMachineMappingRefusesNoJigHome(t *testing.T) {
	if _, err := LoadMachine(""); err == nil {
		t.Error("LoadMachine with no jig home = nil error, want a refusal")
	}
	if err := SaveMachine("", map[string]MachineProject{}); err == nil {
		t.Error("SaveMachine with no jig home = nil error, want a refusal")
	}
}

func TestLoadGateConfigAbsent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gate != nil {
		t.Errorf("Gate = %+v, want nil when gate: is absent", cfg.Gate)
	}
	resolved := cfg.ResolvedGateConfig()
	if *resolved.FixRounds != 3 || *resolved.FixSliceFindings != 5 {
		t.Errorf("ResolvedGateConfig = %+v, unexpected defaults", resolved)
	}
	if len(resolved.FixRisks) != 2 || resolved.FixRisks[0] != "high" || resolved.FixRisks[1] != "medium" {
		t.Errorf("FixRisks = %v, want [high, medium]", resolved.FixRisks)
	}
}

func TestLoadGateConfigWithValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
gate:
  fix_rounds: 5
  fix_risks:
    - high
    - low
  fix_slice_findings: 10
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gate == nil {
		t.Fatalf("Gate = nil, want config")
	}
	if *cfg.Gate.FixRounds != 5 {
		t.Errorf("FixRounds = %d, want 5", *cfg.Gate.FixRounds)
	}
	if *cfg.Gate.FixSliceFindings != 10 {
		t.Errorf("FixSliceFindings = %d, want 10", *cfg.Gate.FixSliceFindings)
	}
	if len(cfg.Gate.FixRisks) != 2 || cfg.Gate.FixRisks[0] != "high" || cfg.Gate.FixRisks[1] != "low" {
		t.Errorf("FixRisks = %v, want [high, low]", cfg.Gate.FixRisks)
	}
}

func TestLoadGateConfigPartialDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	writeFile(t, p, `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
gate:
  fix_rounds: 0
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gate == nil {
		t.Fatalf("Gate = nil, want config")
	}
	if *cfg.Gate.FixRounds != 0 {
		t.Errorf("FixRounds = %d, want 0", *cfg.Gate.FixRounds)
	}
	resolved := cfg.ResolvedGateConfig()
	if *resolved.FixRounds != 0 {
		t.Errorf("Resolved FixRounds = %d, want 0", *resolved.FixRounds)
	}
	if *resolved.FixSliceFindings != 5 {
		t.Errorf("Resolved FixSliceFindings = %d, want default 5", *resolved.FixSliceFindings)
	}
}

func TestLoadGateConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		errMsg  string
	}{
		{
			"negative fix_rounds",
			`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
gate:
  fix_rounds: -1
`,
			"must be non-negative",
		},
		{
			"invalid fix_risks",
			`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
gate:
  fix_risks:
    - high
    - critical
`,
			"invalid risk",
		},
		{
			"fix_slice_findings too small",
			`
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
gate:
  fix_slice_findings: 0
`,
			"must be at least 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "project.yaml")
			writeFile(t, p, tc.content)
			_, err := Load(p)
			if err == nil {
				t.Errorf("Load: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Errorf("Load error = %v, want to contain %q", err, tc.errMsg)
			}
		})
	}
}

// TestLoadEffortConfig: gate.review_effort and builder_effort set the
// reviewer's effort by round scope and a builder's by attempt (ADR 0023);
// an absent level takes its default, an empty one passes none, and a level
// --effort does not accept is refused with the key named.
func TestLoadEffortConfig(t *testing.T) {
	head := `
schema_version: 1
name: demo
ticket_format: "JIG-{n}"
tracker: local
repos: []
platform: platform/
`
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "project.yaml")
		writeFile(t, p, head+body)
		return Load(p)
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := load(t, "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.ReviewEffortFor("full"); got != "high" {
			t.Errorf("full review effort = %q, want high", got)
		}
		if got := cfg.ReviewEffortFor("delta"); got != "medium" {
			t.Errorf("delta review effort = %q, want medium", got)
		}
		if got := cfg.BuilderEffortFor(0); got != "medium" {
			t.Errorf("first attempt effort = %q, want medium", got)
		}
		if got := cfg.BuilderEffortFor(2); got != "high" {
			t.Errorf("retry effort = %q, want high", got)
		}
	})

	t.Run("declared, and empty for none", func(t *testing.T) {
		cfg, err := load(t, `
gate:
  review_effort:
    full: max
    delta: ""
builder_effort:
  first: low
  retry: xhigh
`)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, c := range []struct{ name, got, want string }{
			{"full", cfg.ReviewEffortFor("full"), "max"},
			{"delta", cfg.ReviewEffortFor("delta"), ""},
			{"first", cfg.BuilderEffortFor(0), "low"},
			{"retry", cfg.BuilderEffortFor(1), "xhigh"},
		} {
			if c.got != c.want {
				t.Errorf("%s effort = %q, want %q", c.name, c.got, c.want)
			}
		}
	})

	for _, c := range []struct{ name, body, key string }{
		{"bad review level", "gate:\n  review_effort:\n    full: extreme\n", "gate.review_effort.full"},
		{"bad builder level", "builder_effort:\n  retry: HIGH\n", "builder_effort.retry"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.body)
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Errorf("Load error = %v, want one naming %s", err, c.key)
			}
		})
	}
}
