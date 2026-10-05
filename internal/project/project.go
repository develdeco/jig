// Package project resolves jig's project configuration: the store's
// project.yaml, the per-machine clone mapping, and the two init flows
// (standalone and store+clones).
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// Repo is one repository the project spans.
type Repo struct {
	Remote string `yaml:"remote"`
	Target string `yaml:"target,omitempty"`
}

// TargetBranch returns the branch a ticket's work lands on: Target, or
// "main" when none is configured. Everything that needs the target asks
// here, so the default has one owner.
func (r Repo) TargetBranch() string {
	if r.Target == "" {
		return "main"
	}
	return r.Target
}

// Name returns the repo's short name: the basename of Remote with a
// trailing ".git" removed. Works for both URLs and local filesystem paths.
func (r Repo) Name() string {
	s := strings.TrimSuffix(r.Remote, "/")
	s = strings.TrimSuffix(s, "\\")
	// A remote may be a Windows drive path recorded on another machine, so
	// split on both separators regardless of the host OS.
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	base := filepath.Base(s)
	return strings.TrimSuffix(base, ".git")
}

// GateConfig is the optional gate: block from project.yaml, configuring the
// gate's fix loop and risk floor. Pointers are used for int fields so absent
// values can be distinguished from explicit 0.
type GateConfig struct {
	FixRounds        *int     `yaml:"fix_rounds"`
	FixRisks         []string `yaml:"fix_risks"`
	FixSliceFindings *int     `yaml:"fix_slice_findings"`
}

// DefaultGateConfig returns the config with all defaults applied.
func DefaultGateConfig() GateConfig {
	fr, fsf := 3, 5
	return GateConfig{
		FixRounds:        &fr,
		FixRisks:         []string{"high", "medium"},
		FixSliceFindings: &fsf,
	}
}

// Config is a store's project.yaml.
type Config struct {
	SchemaVersion int
	Name          string
	TicketFormat  string
	Repos         []Repo
	Platform      string
	Staircase     []string
	Context       map[string]any
	// Gate is the optional gate: block configuring the gate's fix loop and
	// risk floor. An absent block is nil; a block with absent keys gains
	// defaults.
	Gate *GateConfig
}

// configRaw mirrors Config's YAML shape, with Tracker, Trackers and Routes
// left as raw nodes so UnmarshalYAML can tell an absent key apart from one
// that is present but empty, and refuse the shapes it no longer accepts.
type configRaw struct {
	SchemaVersion int            `yaml:"schema_version"`
	Name          string         `yaml:"name"`
	TicketFormat  string         `yaml:"ticket_format"`
	Tracker       yaml.Node      `yaml:"tracker"`
	Trackers      yaml.Node      `yaml:"trackers"`
	Repos         []Repo         `yaml:"repos"`
	Platform      string         `yaml:"platform"`
	Staircase     []string       `yaml:"staircase,omitempty"`
	Context       map[string]any `yaml:"context,omitempty"`
	Routes        yaml.Node      `yaml:"routes"`
	Gate          *GateConfig    `yaml:"gate,omitempty"`
}

// UnmarshalYAML decodes project.yaml: trackers: is a list of mirrors (absent
// or empty means none; jig mints every id itself and no mirror shape is
// supported yet, so any entry is refused), tracker: is the key trackers:
// replaces (tracker: local reads as no mirrors, until L3's migration rewrites
// project.yaml; any other value is refused), the two keys together are
// refused, and routes: (which only ever fed publish's now-gone route step)
// is refused outright.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	var raw configRaw
	if err := value.Decode(&raw); err != nil {
		return err
	}
	c.SchemaVersion = raw.SchemaVersion
	c.Name = raw.Name
	c.TicketFormat = raw.TicketFormat
	c.Repos = raw.Repos
	c.Platform = raw.Platform
	c.Staircase = raw.Staircase
	c.Context = raw.Context
	c.Gate = raw.Gate

	if raw.Routes.Kind != 0 {
		return &axi.Error{
			Msg:  "project.yaml declares routes:, which publish no longer consults",
			Code: "VALIDATION_ERROR",
			Help: []string{"Remove routes: from project.yaml: publish renders the pull request body and the review notes itself"},
		}
	}

	trackerPresent, trackersPresent := raw.Tracker.Kind != 0, raw.Trackers.Kind != 0
	if trackerPresent && trackersPresent {
		return &axi.Error{
			Msg:  "project.yaml declares both tracker: and trackers:",
			Code: "VALIDATION_ERROR",
			Help: []string{"trackers: replaces tracker:; remove tracker: from project.yaml"},
		}
	}
	if trackersPresent {
		if raw.Trackers.Kind != yaml.SequenceNode {
			return &axi.Error{
				Msg:  "project.yaml's trackers: is not a list",
				Code: "VALIDATION_ERROR",
				Help: []string{"trackers: takes a list of mirrors, e.g. trackers: []; T-24 builds the tracker tree and T-22 the GitHub mirror"},
			}
		}
		var entries []yaml.Node
		if err := raw.Trackers.Decode(&entries); err != nil {
			return fmt.Errorf("project: decode trackers: %w", err)
		}
		if len(entries) > 0 {
			return &axi.Error{
				Msg:  "project.yaml declares a trackers: entry, which is not supported yet",
				Code: "VALIDATION_ERROR",
				Help: []string{"T-24 builds the tracker tree and T-22 the GitHub mirror; leave trackers: empty until then"},
			}
		}
	}
	if trackerPresent && !(raw.Tracker.Kind == yaml.ScalarNode && raw.Tracker.Value == "local") {
		return &axi.Error{
			Msg:  "project.yaml's tracker: is no longer supported",
			Code: "VALIDATION_ERROR",
			Help: []string{
				"jig mints ids itself; remove tracker: from project.yaml",
				"A GitHub remote with gh on PATH gets its pull requests automatically",
			},
		}
	}
	// tracker: local reads as no mirrors, until L3's migration rewrites
	// project.yaml.

	if err := c.validateGateConfig(); err != nil {
		return err
	}
	return nil
}

// validateGateConfig validates the gate config.
func (c *Config) validateGateConfig() error {
	if c.Gate == nil {
		return nil
	}
	// Validate fix_rounds: can be any non-negative int, negative is refused
	if c.Gate.FixRounds != nil && *c.Gate.FixRounds < 0 {
		return fmt.Errorf("project: gate.fix_rounds must be non-negative, got %d", *c.Gate.FixRounds)
	}
	// Validate fix_risks: each entry must be high, medium, or low
	riskSet := map[string]bool{"high": true, "medium": true, "low": true}
	for _, r := range c.Gate.FixRisks {
		if !riskSet[r] {
			return fmt.Errorf("project: gate.fix_risks contains invalid risk %q, must be high, medium, or low", r)
		}
	}
	// Validate fix_slice_findings: must be >= 1
	if c.Gate.FixSliceFindings != nil && *c.Gate.FixSliceFindings < 1 {
		return fmt.Errorf("project: gate.fix_slice_findings must be at least 1, got %d", *c.Gate.FixSliceFindings)
	}
	return nil
}

// ResolvedGateConfig returns the gate config with defaults applied for any
// absent fields. If Gate is nil, returns the full default config.
func (c Config) ResolvedGateConfig() GateConfig {
	defaults := DefaultGateConfig()
	if c.Gate == nil {
		return defaults
	}
	resolved := *c.Gate
	if resolved.FixRounds == nil {
		resolved.FixRounds = defaults.FixRounds
	}
	if resolved.FixRisks == nil || len(resolved.FixRisks) == 0 {
		resolved.FixRisks = defaults.FixRisks
	}
	if resolved.FixSliceFindings == nil {
		resolved.FixSliceFindings = defaults.FixSliceFindings
	}
	return resolved
}

// projectYAML is the on-disk shape written by InitStandalone: Trackers
// always marshals as "trackers: []" (a nil slice, no omitempty), and no
// tracker: key is ever written.
type projectYAML struct {
	SchemaVersion int      `yaml:"schema_version"`
	Name          string   `yaml:"name"`
	TicketFormat  string   `yaml:"ticket_format"`
	Trackers      []string `yaml:"trackers"`
	Repos         []Repo   `yaml:"repos"`
	Platform      string   `yaml:"platform"`
}

// Load reads and parses a project.yaml file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("project: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("project: parse %s: %w", path, err)
	}
	return cfg, nil
}

// MintLocalID renders the project's ticket format with {n} replaced by n.
func (c Config) MintLocalID(n int) string {
	return strings.ReplaceAll(c.TicketFormat, "{n}", strconv.Itoa(n))
}

// MachineProject is one project's entry in the per-machine mapping
// (JIG_HOME/projects.yaml): where its store lives, and where each of its
// repos is cloned on this machine.
type MachineProject struct {
	Store  string            `yaml:"store"`
	Clones map[string]string `yaml:"clones"`
}

// LoadMachine reads the per-machine project mapping under the jig home root
// jigHome, returning an empty map when the file does not exist yet.
func LoadMachine(jigHome string) (map[string]MachineProject, error) {
	if jigHome == "" {
		return nil, fmt.Errorf("project: no jig home given")
	}
	data, err := os.ReadFile(home.MachinePath(jigHome))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MachineProject{}, nil
		}
		return nil, fmt.Errorf("project: read machine mapping: %w", err)
	}
	m := map[string]MachineProject{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("project: parse machine mapping: %w", err)
	}
	if m == nil {
		m = map[string]MachineProject{}
	}
	return m, nil
}

// SaveMachine writes the per-machine project mapping under the jig home root
// jigHome, creating its parent directory if needed.
func SaveMachine(jigHome string, m map[string]MachineProject) error {
	if jigHome == "" {
		return fmt.Errorf("project: no jig home given")
	}
	path := home.MachinePath(jigHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("project: create home dir: %w", err)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("project: marshal machine mapping: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("project: write machine mapping: %w", err)
	}
	return nil
}

// Resolve locates the store for cwd: an explicit --store flag wins, then a
// project.yaml directly in cwd, then a clone that contains cwd in the
// machine mapping under the jig home root jigHome.
func Resolve(jigHome, cwd, storeFlag string) (string, Config, error) {
	storePath, err := resolveStorePath(jigHome, cwd, storeFlag)
	if err != nil {
		return "", Config{}, err
	}
	cfg, err := Load(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		return "", Config{}, err
	}
	return storePath, cfg, nil
}

func resolveStorePath(jigHome, cwd, storeFlag string) (string, error) {
	if storeFlag != "" {
		return storeFlag, nil
	}
	if _, err := os.Stat(filepath.Join(cwd, "project.yaml")); err == nil {
		return cwd, nil
	}
	machine, err := LoadMachine(jigHome)
	if err != nil {
		return "", err
	}
	for _, mp := range machine {
		for _, clone := range mp.Clones {
			if pathContains(clone, cwd) {
				return mp.Store, nil
			}
		}
	}
	// Last resort: the store `jig init --standalone` creates next to this
	// directory, so commands work from the repo it was initialized for.
	if sibling, err := StandaloneStoreDir(cwd); err == nil {
		if _, err := os.Stat(filepath.Join(sibling, "project.yaml")); err == nil {
			return sibling, nil
		}
	}
	return "", &axi.Error{
		Msg:  "no jig store found for this directory: pass --store or run inside a store or a mapped clone",
		Code: "VALIDATION_ERROR",
	}
}

// pathContains reports whether target is base itself or a descendant of it.
func pathContains(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// StandaloneStoreDir returns the sibling "<repoDir base>-tickets" path
// InitStandalone creates its store at.
func StandaloneStoreDir(repoDir string) (string, error) {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("project: resolve repo dir: %w", err)
	}
	return siblingStoreDir(absRepo), nil
}

func siblingStoreDir(absRepo string) string {
	return filepath.Join(filepath.Dir(absRepo), filepath.Base(absRepo)+"-tickets")
}

// InitStandalone creates a sibling "<repoDir base>-tickets" store next to
// repoDir: a fresh git repo on branch main, project.yaml pointing back at
// repoDir, an empty platform/ dir, and an empty ledger.md. It returns the
// new store's path. Run against an existing store, it resets project.yaml
// and ledger.md.
func InitStandalone(repoDir string) (string, error) {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("project: resolve repo dir: %w", err)
	}
	base := filepath.Base(absRepo)
	storeDir := siblingStoreDir(absRepo)

	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return "", fmt.Errorf("project: create store dir: %w", err)
	}
	if _, err := gitx.Run(storeDir, "init", "-b", "main"); err != nil {
		return "", fmt.Errorf("project: git init: %w", err)
	}

	py := projectYAML{
		SchemaVersion: 1,
		Name:          base,
		TicketFormat:  "T-{n}",
		Repos:         []Repo{{Remote: absRepo}},
		Platform:      "platform/",
	}
	data, err := yaml.Marshal(py)
	if err != nil {
		return "", fmt.Errorf("project: marshal project.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "project.yaml"), data, 0o644); err != nil {
		return "", fmt.Errorf("project: write project.yaml: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "platform"), 0o755); err != nil {
		return "", fmt.Errorf("project: create platform dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "ledger.md"), []byte{}, 0o644); err != nil {
		return "", fmt.Errorf("project: write ledger.md: %w", err)
	}
	// store.Lock's sidecar "*.lock" files (never removed) and
	// store.AtomicWrite's ".*.tmp" scratch files are noise the truth repo
	// must never track: ignore them at the store root so locked writes never
	// dirty git status or collide with another clone's own lock files.
	if err := os.WriteFile(filepath.Join(storeDir, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		return "", fmt.Errorf("project: write .gitignore: %w", err)
	}
	// No conversion of any file in the store: its files are jig's data, and
	// it lets gitx work on the store in process (gitx.Repo).
	if err := os.WriteFile(filepath.Join(storeDir, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		return "", fmt.Errorf("project: write .gitattributes: %w", err)
	}

	// Commit the scaffold itself, under jig's own identity rather than
	// whatever (if anything) the host's git config holds: every later write
	// - store.Claim's claims in particular - stages and commits only the
	// paths it touches, and so does this one, scoped to the scaffold's own
	// paths rather than a sweeping `add -A` that would catch whatever else
	// is dirty in an existing store. The re-init path's reset can leave
	// nothing staged (project.yaml byte-identical, ledger.md already
	// empty), which is success, not a failure to report.
	if _, err := gitx.Run(storeDir, "add", "-A", "--", "project.yaml", "platform", "ledger.md", ".gitignore", ".gitattributes"); err != nil {
		return "", fmt.Errorf("project: stage store scaffold: %w", err)
	}
	if staged, err := gitx.Run(storeDir, "diff", "--cached", "--name-only"); err != nil {
		return "", fmt.Errorf("project: check staged store scaffold: %w", err)
	} else if staged != "" {
		if _, err := gitx.Run(storeDir, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "jig: init store"); err != nil {
			return "", fmt.Errorf("project: commit store scaffold: %w", err)
		}
	}

	return storeDir, nil
}

// InitProject loads storePath's project.yaml, validates that every clone
// name matches a repo declared there (by Repo.Name), and records the
// mapping in the per-machine store under the jig home root jigHome, keyed by
// the project's name.
func InitProject(jigHome, storePath string, clones map[string]string) (Config, error) {
	cfg, err := Load(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		return Config{}, err
	}

	repoNames := map[string]bool{}
	for _, r := range cfg.Repos {
		repoNames[r.Name()] = true
	}
	for name := range clones {
		if !repoNames[name] {
			return Config{}, &axi.Error{
				Msg:  fmt.Sprintf("clone %q does not match any repo declared in project.yaml", name),
				Code: "VALIDATION_ERROR",
			}
		}
	}

	absStore, err := filepath.Abs(storePath)
	if err != nil {
		return Config{}, fmt.Errorf("project: resolve store path: %w", err)
	}
	absClones := make(map[string]string, len(clones))
	for name, p := range clones {
		abs, err := filepath.Abs(p)
		if err != nil {
			return Config{}, fmt.Errorf("project: resolve clone %s: %w", name, err)
		}
		absClones[name] = abs
	}

	machine, err := LoadMachine(jigHome)
	if err != nil {
		return Config{}, err
	}
	machine[cfg.Name] = MachineProject{Store: absStore, Clones: absClones}
	if err := SaveMachine(jigHome, machine); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
