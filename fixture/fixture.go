// Package fixture materializes the jig fixture repo, its truth-repo store,
// and a scripted attempt/patch scenario, all rooted in a fresh temp
// directory per Generate call. Every other package's tests build on this
// fixture instead of hand-rolling git repos.
//
// Callers must set JIG_HOME (typically t.Setenv("JIG_HOME", t.TempDir()))
// before calling Generate. Generate itself never touches the environment; it
// writes the per-machine project mapping under whatever JIG_HOME the caller
// already configured, and returns the paths it created.
package fixture

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/gittest"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/manifest"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/store"
)

// Ticket is the single ticket id every generated fixture store carries.
const Ticket = "JIG-1"

// Fixture is one materialized instance of the jig fixture.
type Fixture struct {
	Dir         string // temp root everything else lives under
	RepoDir     string // working clone of the fixture repo (committed, on main)
	RepoRemote  string // bare remote for the fixture repo
	StoreDir    string // truth-repo checkout (project.yaml, ledger.md, JIG-1/)
	StoreRemote string // bare remote for the store; "" when Opts.Standalone
	ScenarioDir string // materialized scenario tree for the fake session backend
	Ticket      string // "JIG-1"
}

// Opts configures a Generate call.
type Opts struct {
	// Standalone omits the store's remote and origin, matching a store with
	// no shared truth repo.
	Standalone bool
	// ScenarioBranch names a scenario-branches/<name> overlay applied over
	// the base scenario tree, file by file. "" uses the base scenario as
	// committed.
	ScenarioBranch string
	// EnvFail inserts " --fail" into the rig environment class's up command,
	// so envtool exits 1 before writing its state file.
	EnvFail bool
}

// identityEnv pins the git author/committer identity and date used for every
// commit Generate makes, so fixtures are byte-for-byte reproducible.
var identityEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// Generate materializes a fresh fixture into a new t.TempDir() and returns
// it. It fails the test via t.Fatal on any setup error.
func Generate(t *testing.T, opts Opts) *Fixture {
	t.Helper()

	root := t.TempDir()
	testdataDir := testdataFixtureDir(t)

	envtoolBin := buildEnvtool(t, testdataDir)
	stateFile := filepath.Join(root, "rig-state.txt")

	repoDir := filepath.Join(root, "fixture-repo")
	copyTree(t, filepath.Join(testdataDir, "fixture-repo"), repoDir)
	rewriteJigYAML(t, repoDir, envtoolBin, stateFile, opts.EnvFail)
	initGitRepo(t, repoDir)
	commitAll(t, repoDir, "fixture: initial alpha/beta workspaces")

	repoRemote := filepath.Join(root, "fixture-repo.git")
	runGit(t, root, "clone", "--bare", repoDir, repoRemote)
	runGit(t, repoDir, "remote", "add", "origin", repoRemote)

	storeDir := filepath.Join(root, "store")
	buildStore(t, storeDir, testdataDir, repoRemote)
	initGitRepo(t, storeDir)
	commitAll(t, storeDir, "fixture: initial store")

	var storeRemote string
	if !opts.Standalone {
		storeRemote = filepath.Join(root, "store.git")
		runGit(t, root, "clone", "--bare", storeDir, storeRemote)
		runGit(t, storeDir, "remote", "add", "origin", storeRemote)
	}

	if _, err := project.InitProject(storeDir, map[string]string{"fixture-repo": repoDir}); err != nil {
		t.Fatalf("fixture: init project machine mapping: %v", err)
	}

	scenarioDir := filepath.Join(root, "scenario")
	copyTree(t, filepath.Join(testdataDir, "scenario"), scenarioDir)
	if opts.ScenarioBranch != "" {
		branchDir := filepath.Join(testdataDir, "scenario-branches", opts.ScenarioBranch)
		if _, err := os.Stat(branchDir); err != nil {
			t.Fatalf("fixture: unknown scenario branch %q: %v", opts.ScenarioBranch, err)
		}
		copyTree(t, branchDir, scenarioDir)
	}

	return &Fixture{
		Dir:         root,
		RepoDir:     repoDir,
		RepoRemote:  repoRemote,
		StoreDir:    storeDir,
		StoreRemote: storeRemote,
		ScenarioDir: scenarioDir,
		Ticket:      Ticket,
	}
}

// buildStore writes the store's project.yaml, ledger.md, platform/ dir, and
// the JIG-1 ticket folder (brief.md + slices.yaml with @HASH placeholders
// resolved against the brief's own section hashes).
func buildStore(t *testing.T, storeDir, testdataDir, repoRemote string) {
	t.Helper()
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("fixture: create store dir: %v", err)
	}

	cfg := storeProjectYAML{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "JIG-{n}",
		Tracker:       "local",
		Repos:         []project.Repo{{Remote: repoRemote, Target: "main"}},
		Platform:      "platform/",
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("fixture: marshal project.yaml: %v", err)
	}
	writeFile(t, filepath.Join(storeDir, "project.yaml"), data)
	writeFile(t, filepath.Join(storeDir, "ledger.md"), []byte("# Ledger\n"))
	if err := os.MkdirAll(filepath.Join(storeDir, "platform"), 0o755); err != nil {
		t.Fatalf("fixture: create platform dir: %v", err)
	}
	// Mirrors project.InitStandalone's store-root .gitignore: store.Lock's
	// sidecar "*.lock" files and store.AtomicWrite's ".*.tmp" scratch files
	// must never show up as untracked/dirty in a fixture store either.
	writeFile(t, filepath.Join(storeDir, ".gitignore"), []byte("*.lock\n.*.tmp\n"))

	briefSrc, err := os.ReadFile(filepath.Join(testdataDir, "brief.md"))
	if err != nil {
		t.Fatalf("fixture: read brief.md: %v", err)
	}
	brief := normalizeNewlines(briefSrc)
	hashes := store.BriefSectionHashes(brief)

	slicesSrc, err := os.ReadFile(filepath.Join(testdataDir, "slices.yaml"))
	if err != nil {
		t.Fatalf("fixture: read slices.yaml: %v", err)
	}
	slices := resolveHashPlaceholders(t, normalizeNewlines(slicesSrc), hashes)

	ticketDir := filepath.Join(storeDir, Ticket)
	if err := os.MkdirAll(ticketDir, 0o755); err != nil {
		t.Fatalf("fixture: create ticket dir: %v", err)
	}
	writeFile(t, filepath.Join(ticketDir, "brief.md"), brief)
	writeFile(t, filepath.Join(ticketDir, "slices.yaml"), slices)
}

// storeProjectYAML is the on-disk shape Generate writes for the fixture
// store's project.yaml; it mirrors project.Config's declared wire format.
type storeProjectYAML struct {
	SchemaVersion int            `yaml:"schema_version"`
	Name          string         `yaml:"name"`
	TicketFormat  string         `yaml:"ticket_format"`
	Tracker       string         `yaml:"tracker"`
	Repos         []project.Repo `yaml:"repos"`
	Platform      string         `yaml:"platform"`
}

// hashPlaceholder matches slices.yaml's committed @HASH:<heading> markers.
var hashPlaceholder = regexp.MustCompile(`@HASH:([^"]+)`)

// resolveHashPlaceholders replaces every @HASH:<heading> marker in data with
// the matching brief section's hash.
func resolveHashPlaceholders(t *testing.T, data []byte, hashes map[string]string) []byte {
	t.Helper()
	var missing string
	out := hashPlaceholder.ReplaceAllFunc(data, func(m []byte) []byte {
		heading := strings.TrimSpace(string(hashPlaceholder.FindSubmatch(m)[1]))
		h, ok := hashes[heading]
		if !ok {
			missing = heading
			return m
		}
		return []byte(h)
	})
	if missing != "" {
		t.Fatalf("fixture: no brief section hash for heading %q", missing)
	}
	return out
}

// rewriteJigYAML replaces the fixture repo's @ENVTOOL/@STATEFILE placeholders
// with the built envtool binary and a per-fixture state file path, and
// optionally makes the rig class's up command fail before writing state.
func rewriteJigYAML(t *testing.T, repoDir, envtoolBin, stateFile string, envFail bool) {
	t.Helper()
	path := filepath.Join(repoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: read jig.yaml: %v", err)
	}
	var m manifest.Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("fixture: parse jig.yaml: %v", err)
	}
	subst := func(s string) string {
		s = strings.ReplaceAll(s, "@ENVTOOL", quoteIfSpaced(filepath.ToSlash(envtoolBin)))
		s = strings.ReplaceAll(s, "@STATEFILE", filepath.ToSlash(stateFile))
		s = strings.ReplaceAll(s, "@GO", quoteIfSpaced(filepath.ToSlash(goBinaryPath())))
		return s
	}
	for name, cmd := range m.Oracles {
		m.Oracles[name] = subst(cmd)
	}
	for name, ec := range m.Envs {
		ec.Up, ec.Check, ec.Down = subst(ec.Up), subst(ec.Check), subst(ec.Down)
		m.Envs[name] = ec
	}
	if envFail {
		for name, ec := range m.Envs {
			ec.Up = strings.Replace(ec.Up, " up ", " up --fail ", 1)
			m.Envs[name] = ec
		}
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("fixture: marshal jig.yaml: %v", err)
	}
	writeFile(t, path, out)
}

// The envtool helper is built once per test binary and shared by every
// Generate call.
var (
	envtoolOnce     sync.Once
	envtoolBin      string
	envtoolBuildErr error
)

// buildEnvtool returns the path of the once-built envtool helper. It lives in
// its own temp dir (a t.TempDir would vanish with the first test that used
// it), removed through gittest.AtExit when the test binary finishes; the
// generated jig.yaml refers to it by absolute path. A build failure fails
// every caller with the original error.
func buildEnvtool(t *testing.T, testdataDir string) string {
	t.Helper()
	envtoolOnce.Do(func() {
		dir, err := os.MkdirTemp("", "jig-envtool")
		if err != nil {
			envtoolBuildErr = fmt.Errorf("fixture: create envtool build dir: %w", err)
			return
		}
		gittest.AtExit(func() { os.RemoveAll(dir) })

		out := filepath.Join(dir, "envtool"+exeSuffix())
		goBin := filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())

		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, ".")
		cmd.Dir = filepath.Join(testdataDir, "envtool")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			envtoolBuildErr = fmt.Errorf("fixture: build envtool: %w\n%s", err, outBytes)
			return
		}
		envtoolBin = out
	})
	if envtoolBuildErr != nil {
		t.Fatalf("%v", envtoolBuildErr)
	}
	return envtoolBin
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testdataFixtureDir locates the product repo's testdata/fixture directory
// relative to this source file, so Generate works regardless of the
// caller's own working directory.
func testdataFixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture: cannot determine source file location")
	}
	// This file lives at <productRoot>/fixture/fixture.go.
	productRoot := filepath.Dir(filepath.Dir(file))
	return filepath.Join(productRoot, "testdata", "fixture")
}

// copyTree recursively copies src onto dst, normalizing line endings in
// every file to "\n" and creating directories (including empty ones) as it
// goes. Existing files at the destination are overwritten, so copyTree also
// serves as the branch-overlay mechanism: calling it a second time with a
// scenario-branches/<name> source layers that branch's files over the base
// scenario tree.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFileErr(target, normalizeNewlines(data))
	})
	if err != nil {
		t.Fatalf("fixture: copy %s -> %s: %v", src, dst, err)
	}
}

func writeFileErr(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := writeFileErr(path, data); err != nil {
		t.Fatalf("fixture: write %s: %v", path, err)
	}
}

func normalizeNewlines(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitx.Run(dir, args...); err != nil {
		t.Fatalf("fixture: git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "jig-fixture")
	runGit(t, dir, "config", "user.email", "fixture@example.invalid")
	runGit(t, dir, "config", "core.autocrlf", "false")
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, identityEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("fixture: git commit (in %s): %v", dir, err)
	}
}

// goBinaryPath returns the absolute path to the go tool used to build and
// exercise the fixture, honoring the same GOROOT the running test binary was
// built with.
func goBinaryPath() string {
	return filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())
}

// quoteIfSpaced wraps a command path in double quotes when it contains a
// space, so platform-shell command strings stay parseable.
func quoteIfSpaced(p string) string {
	if strings.Contains(p, " ") {
		return "\"" + p + "\""
	}
	return p
}
