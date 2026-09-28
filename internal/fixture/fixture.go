// Package fixture materializes the jig fixture repo, its truth-repo store,
// and a scripted attempt/patch scenario, all rooted in a caller-chosen
// directory. Build(dir, opts) does the work and returns an error; every
// helper it calls returns an error too, so Build never calls into testing
// and can be called from any binary, not only a test.
// Generate is a thin wrapper for tests: it builds into a fresh t.TempDir()
// and fails the test via t.Fatal. Every other package's tests build on this
// fixture instead of hand-rolling git repos.
//
// "Any binary" means one built from this module checkout without
// -trimpath. -trimpath has two effects here: it clears runtime.GOROOT(),
// which buildEnvtool needs to find the go tool it invokes to build the
// envtool helper, and it replaces this file's compile-time path with its
// import path, so testdataFixtureDir then finds go.mod only when the binary
// runs from the module root.
//
// Build and Generate write the per-machine project mapping under Opts.Home,
// the jig home root the caller hands the code under test (a test passes its
// own t.TempDir()). Left empty, Home is home.Root(): JIG_HOME, else the real
// home directory, as the jig binary resolves it, so a caller that leaves it
// empty must set JIG_HOME first. Neither touches the environment itself.
package fixture

import (
	"errors"
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

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// Ticket is the single ticket id every generated fixture store carries.
const Ticket = "JIG-1"

// ErrUnknownScenarioBranch wraps the error Build returns when Opts.ScenarioBranch
// names an overlay that does not exist under testdata/fixture/scenario-branches.
// Callers that need to tell this apart from any other failure (a copyTree error
// can also mention a scenario-branches path) should check it with errors.Is,
// not by matching the error text.
var ErrUnknownScenarioBranch = errors.New("fixture: unknown scenario branch")

// Fixture is one materialized instance of the jig fixture.
type Fixture struct {
	Dir         string // root everything else lives under
	RepoDir     string // working clone of the fixture repo (committed, on main)
	RepoRemote  string // bare remote for the fixture repo
	StoreDir    string // truth-repo checkout (project.yaml, ledger.md, JIG-1/)
	StoreRemote string // bare remote for the store; "" when Opts.Standalone
	ScenarioDir string // materialized scenario tree for the fake session backend
	Ticket      string // "JIG-1"
	Home        string // jig home root the machine mapping was written under
}

// Opts configures a Build or Generate call.
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
	// Home is the jig home root the per-machine project mapping is written
	// under. "" means home.Root() (JIG_HOME, else the real home directory).
	Home string
}

// identityEnv pins the git author/committer identity and date used for every
// commit Build makes, so fixtures are byte-for-byte reproducible.
var identityEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// Build materializes a fresh fixture into dir and returns it. dir is
// resolved to an absolute path first (so every path Build derives from it,
// and every git command Build runs, is unambiguous regardless of the
// caller's own working directory), then created if it does not exist; an
// existing, non-empty dir is refused. Every helper Build calls returns an
// error rather than failing a test, so Build never calls into testing and
// can run from any binary.
//
// Everything checkable is validated before writing the fixture, and the
// per-machine mapping under the jig home (project.InitProject) is written
// last, after everything else under dir already exists. On any error once the
// non-empty check has passed, Build removes what it wrote: dir's new
// contents, and dir itself when Build created it, so a retry into the same
// dir is not refused unless the cleanup itself fails, which Build reports.
func Build(dir string, opts Opts) (fx *Fixture, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("fixture: resolve %s: %w", dir, err)
	}
	dir = abs

	empty, created, err := dirEmpty(dir)
	if err != nil {
		return nil, err
	}
	if !empty {
		return nil, fmt.Errorf("fixture: %s is not empty", dir)
	}
	defer func() {
		if err == nil {
			return
		}
		var rmErr error
		if created {
			rmErr = os.RemoveAll(dir)
		} else {
			rmErr = clearDirContents(dir)
		}
		if rmErr != nil {
			err = errors.Join(err, fmt.Errorf("fixture: clean up %s: %w", dir, rmErr))
		}
	}()

	testdataDir, err := testdataFixtureDir()
	if err != nil {
		return nil, err
	}

	var branchDir string
	if opts.ScenarioBranch != "" {
		branchDir = filepath.Join(testdataDir, "scenario-branches", opts.ScenarioBranch)
		if _, statErr := os.Stat(branchDir); statErr != nil {
			return nil, fmt.Errorf("%w %q: %w", ErrUnknownScenarioBranch, opts.ScenarioBranch, statErr)
		}
	}

	cachedEnvtool, err := buildEnvtool(testdataDir)
	if err != nil {
		return nil, err
	}
	envtoolBin := filepath.Join(dir, "bin", "envtool"+exeSuffix())
	if err := copyFile(cachedEnvtool, envtoolBin, 0o755); err != nil {
		return nil, err
	}
	stateFile := filepath.Join(dir, "rig-state.txt")

	repoDir := filepath.Join(dir, "fixture-repo")
	if err := copyTree(filepath.Join(testdataDir, "fixture-repo"), repoDir); err != nil {
		return nil, err
	}
	if err := rewriteJigYAML(repoDir, envtoolBin, stateFile, opts.EnvFail); err != nil {
		return nil, err
	}
	if err := initGitRepo(repoDir); err != nil {
		return nil, err
	}
	if err := commitAll(repoDir, "fixture: initial alpha/beta workspaces"); err != nil {
		return nil, err
	}

	repoRemote := filepath.Join(dir, "fixture-repo.git")
	if err := runGit(dir, "clone", "--bare", repoDir, repoRemote); err != nil {
		return nil, err
	}
	if err := runGit(repoDir, "remote", "add", "origin", repoRemote); err != nil {
		return nil, err
	}

	storeDir := filepath.Join(dir, "store")
	if err := buildStore(storeDir, testdataDir, repoRemote); err != nil {
		return nil, err
	}
	if err := initGitRepo(storeDir); err != nil {
		return nil, err
	}
	if err := commitAll(storeDir, "fixture: initial store"); err != nil {
		return nil, err
	}

	var storeRemote string
	if !opts.Standalone {
		storeRemote = filepath.Join(dir, "store.git")
		if err := runGit(dir, "clone", "--bare", storeDir, storeRemote); err != nil {
			return nil, err
		}
		if err := runGit(storeDir, "remote", "add", "origin", storeRemote); err != nil {
			return nil, err
		}
	}

	scenarioDir := filepath.Join(dir, "scenario")
	if err := copyTree(filepath.Join(testdataDir, "scenario"), scenarioDir); err != nil {
		return nil, err
	}
	if branchDir != "" {
		if err := copyTree(branchDir, scenarioDir); err != nil {
			return nil, err
		}
	}

	jigHome := opts.Home
	if jigHome == "" {
		if jigHome, err = home.Root(); err != nil {
			return nil, fmt.Errorf("fixture: resolve jig home: %w", err)
		}
	}
	if _, err := project.InitProject(jigHome, storeDir, map[string]string{"fixture-repo": repoDir}); err != nil {
		return nil, fmt.Errorf("fixture: init project machine mapping: %w", err)
	}

	return &Fixture{
		Dir:         dir,
		RepoDir:     repoDir,
		RepoRemote:  repoRemote,
		StoreDir:    storeDir,
		StoreRemote: storeRemote,
		ScenarioDir: scenarioDir,
		Ticket:      Ticket,
		Home:        jigHome,
	}, nil
}

// Generate materializes a fresh fixture into a new t.TempDir() and returns
// it. It fails the test via t.Fatal on any setup error. With neither
// opts.Home nor JIG_HOME set, the machine mapping goes to another
// t.TempDir(), never the real home directory Build would fall back to.
func Generate(t *testing.T, opts Opts) *Fixture {
	t.Helper()
	if opts.Home == "" && os.Getenv("JIG_HOME") == "" {
		// Build would fall back to the real home directory; a test never
		// writes there.
		opts.Home = t.TempDir()
	}
	fx, err := Build(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

// dirEmpty reports whether dir is empty, creating it first if it does not
// exist yet, and whether it had to be created (so Build knows to remove dir
// itself, not just its contents, if a later step fails). Creating dir uses
// MkdirAll, so any missing parent directories are created too; Build's own
// cleanup only ever removes dir itself (or, when dir already existed, dir's
// contents), so parents that MkdirAll created are left behind on failure.
func dirEmpty(dir string) (empty, created bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return false, false, fmt.Errorf("fixture: create %s: %w", dir, err)
			}
			return true, true, nil
		}
		return false, false, fmt.Errorf("fixture: read %s: %w", dir, err)
	}
	return len(entries) == 0, false, nil
}

// clearDirContents removes every entry directly under dir without removing
// dir itself: Build's cleanup for a dir that already existed (and was
// empty) before a failed Build wrote into it. It keeps removing every
// entry even after one fails, but returns the first removal error so Build
// can report a cleanup that did not fully succeed, instead of leaving a
// non-empty dir behind with no signal that anything is wrong.
func clearDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if rmErr := os.RemoveAll(filepath.Join(dir, e.Name())); rmErr != nil && firstErr == nil {
			firstErr = rmErr
		}
	}
	return firstErr
}

// buildStore writes the store's project.yaml, ledger.md, platform/ dir, and
// the JIG-1 ticket folder (brief.md + slices.yaml with @HASH placeholders
// resolved against the brief's own section hashes).
func buildStore(storeDir, testdataDir, repoRemote string) error {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return fmt.Errorf("fixture: create store dir: %w", err)
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
		return fmt.Errorf("fixture: marshal project.yaml: %w", err)
	}
	if err := writeFile(filepath.Join(storeDir, "project.yaml"), data); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(storeDir, "ledger.md"), []byte("# Ledger\n")); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "platform"), 0o755); err != nil {
		return fmt.Errorf("fixture: create platform dir: %w", err)
	}
	// Mirrors project.InitStandalone's store-root .gitignore: store.Lock's
	// sidecar "*.lock" files and store.AtomicWrite's ".*.tmp" scratch files
	// must never show up as untracked/dirty in a fixture store either.
	if err := writeFile(filepath.Join(storeDir, ".gitignore"), []byte("*.lock\n.*.tmp\n")); err != nil {
		return err
	}
	// And its .gitattributes: no line-ending conversion in the store.
	if err := writeFile(filepath.Join(storeDir, ".gitattributes"), []byte(gitx.StoreAttributes)); err != nil {
		return err
	}

	briefSrc, err := os.ReadFile(filepath.Join(testdataDir, "brief.md"))
	if err != nil {
		return fmt.Errorf("fixture: read brief.md: %w", err)
	}
	brief := normalizeNewlines(briefSrc)
	hashes := store.BriefSectionHashes(brief)

	slicesSrc, err := os.ReadFile(filepath.Join(testdataDir, "slices.yaml"))
	if err != nil {
		return fmt.Errorf("fixture: read slices.yaml: %w", err)
	}
	slices, err := resolveHashPlaceholders(normalizeNewlines(slicesSrc), hashes)
	if err != nil {
		return err
	}

	ticketDir := filepath.Join(storeDir, Ticket)
	if err := os.MkdirAll(ticketDir, 0o755); err != nil {
		return fmt.Errorf("fixture: create ticket dir: %w", err)
	}
	if err := writeFile(filepath.Join(ticketDir, "brief.md"), brief); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(ticketDir, "slices.yaml"), slices); err != nil {
		return err
	}
	return nil
}

// storeProjectYAML is the on-disk shape Build writes for the fixture
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
func resolveHashPlaceholders(data []byte, hashes map[string]string) ([]byte, error) {
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
		return nil, fmt.Errorf("fixture: no brief section hash for heading %q", missing)
	}
	return out, nil
}

// rewriteJigYAML replaces the fixture repo's @ENVTOOL/@STATEFILE placeholders
// with the built envtool binary and a per-fixture state file path, and
// optionally makes the rig class's up command fail before writing state.
func rewriteJigYAML(repoDir, envtoolBin, stateFile string, envFail bool) error {
	path := filepath.Join(repoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("fixture: read jig.yaml: %w", err)
	}
	var m manifest.Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("fixture: parse jig.yaml: %w", err)
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
		return fmt.Errorf("fixture: marshal jig.yaml: %w", err)
	}
	return writeFile(path, out)
}

// The envtool helper is built once per process into a shared cache dir; each
// Build call copies that cached binary into its own dir/bin (see Build), so
// a fixture no longer references that shared, longer-lived cache. The
// oracle command Build writes into jig.yaml still runs the Go toolchain at
// runtime.GOROOT() (see goBinaryPath), a path outside dir.
var (
	envtoolOnce     sync.Once
	envtoolBin      string
	envtoolBuildErr error
)

// buildEnvtool returns the path of the once-built envtool helper, cached for
// the life of the process in its own temp dir (a caller-chosen dir might be
// removed or reused between Build calls, so the cache cannot live there). A
// build failure fails every caller with the original error. Inside a test
// binary the cache dir is removed through gittest.AtExit when the binary
// finishes; outside one (gittest.Run never runs), it is a one-per-process
// leak of a build artifact, not of any fixture's own output, since Build
// copies the binary out before returning. The OS does not reclaim it on
// Windows, so a long-lived non-test process calling Build many times should
// expect one leaked temp dir for its own lifetime, not one per call.
func buildEnvtool(testdataDir string) (string, error) {
	envtoolOnce.Do(func() {
		goroot := runtime.GOROOT()
		if goroot == "" {
			envtoolBuildErr = errors.New("fixture: no GOROOT; build the caller without -trimpath or set GOROOT")
			return
		}

		dir, err := os.MkdirTemp("", "jig-envtool")
		if err != nil {
			envtoolBuildErr = fmt.Errorf("fixture: create envtool build dir: %w", err)
			return
		}
		gittest.AtExit(func() { os.RemoveAll(dir) })

		out := filepath.Join(dir, "envtool"+exeSuffix())
		goBin := filepath.Join(goroot, "bin", "go"+exeSuffix())

		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, ".")
		cmd.Dir = filepath.Join(testdataDir, "envtool")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			envtoolBuildErr = fmt.Errorf("fixture: build envtool: %w\n%s", err, outBytes)
			return
		}
		envtoolBin = out
	})
	return envtoolBin, envtoolBuildErr
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testdataFixtureDir locates the product repo's testdata/fixture directory,
// so Build works regardless of its caller's own location or working
// directory.
func testdataFixtureDir() (string, error) {
	root, err := repoRootFrom(0)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "testdata", "fixture"), nil
}

// RepoRoot returns the module root: the directory containing go.mod. It
// walks up from the source file of RepoRoot's caller, so it keeps working
// no matter how deep in the tree that caller's package lives.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := repoRootFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// repoRootFrom returns the module root: the directory containing go.mod,
// walking up from the source file located skip call-frames above
// repoRootFrom's own caller. skip=0 means "repoRootFrom's direct caller"
// (used internally, always resolving to this file); RepoRoot passes skip=1
// to reach its own caller's source file instead.
func repoRootFrom(skip int) (string, error) {
	_, file, _, ok := runtime.Caller(skip + 1)
	if !ok {
		return "", fmt.Errorf("fixture: cannot determine caller's source file location")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("fixture: no go.mod found above %s", file)
		}
		dir = parent
	}
}

// copyTree recursively copies src onto dst, normalizing line endings in
// every file to "\n" and creating directories (including empty ones) as it
// goes. Existing files at the destination are overwritten, so copyTree also
// serves as the branch-overlay mechanism: calling it a second time with a
// scenario-branches/<name> source layers that branch's files over the base
// scenario tree.
func copyTree(src, dst string) error {
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
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, normalizeNewlines(data), 0o644)
	})
	if err != nil {
		return fmt.Errorf("fixture: copy %s -> %s: %w", src, dst, err)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("fixture: write %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("fixture: write %s: %w", path, err)
	}
	return nil
}

// copyFile copies src onto dst with dst's permissions set to perm, creating
// dst's parent directories as needed. Unlike writeFile (always 0o644), the
// caller picks perm, since a copied executable needs its exec bit.
func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("fixture: read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("fixture: create %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		return fmt.Errorf("fixture: write %s: %w", dst, err)
	}
	return nil
}

func normalizeNewlines(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

// hermeticGitEnv keeps Build's own git calls unaffected by the host's config
// files (system and global: commit signing, hooks, an unusual default
// branch, and so on), so a fixture is byte-for-byte reproducible from any
// binary, not only a test binary, where gittest.Run does the same job for
// the whole process. It does not shield against git config set through the
// environment itself (GIT_CONFIG_COUNT/GIT_CONFIG_PARAMETERS, GIT_TEMPLATE_DIR,
// GIT_DEFAULT_HASH, and the like); a caller whose own environment sets those
// still affects Build's git calls.
var hermeticGitEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=" + os.DevNull,
}

func runGit(dir string, args ...string) error {
	if _, err := gitx.RunEnv(dir, hermeticGitEnv, args...); err != nil {
		return fmt.Errorf("fixture: git %s (in %s): %w", strings.Join(args, " "), dir, err)
	}
	return nil
}

func initGitRepo(dir string) error {
	if err := runGit(dir, "init", "-b", "main"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "user.name", "jig-fixture"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "user.email", "fixture@example.invalid"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "core.autocrlf", "false"); err != nil {
		return err
	}
	return nil
}

func commitAll(dir, msg string) error {
	if err := runGit(dir, "add", "-A"); err != nil {
		return err
	}
	env := append(append([]string{}, hermeticGitEnv...), identityEnv...)
	if _, err := gitx.RunEnv(dir, env, "commit", "-m", msg); err != nil {
		return fmt.Errorf("fixture: git commit (in %s): %w", dir, err)
	}
	return nil
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
