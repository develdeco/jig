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

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/home"
)

// Repo is one repository the project spans.
type Repo struct {
	Remote string `yaml:"remote"`
	Target string `yaml:"target,omitempty"`
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

// Config is a store's project.yaml.
type Config struct {
	SchemaVersion int
	Name          string
	TicketFormat  string
	// Tracker is "local", "github", "jira", "linear", or "command"; it and
	// TrackerCmd are populated from the YAML "tracker" key, which may be a
	// plain string or a {command: <path>} map (see UnmarshalYAML).
	Tracker    string
	TrackerCmd string
	Repos      []Repo
	Platform   string
	Staircase  []string
	Context    map[string]any
	// Routes is the declared routing map publish's route step consults: keys
	// "pr.description", "pr.comments" and "ticket.comments", values being
	// store-relative path globs. A nil/empty map (the common case) means
	// "use the spec's defaults", applied by the caller - Config itself
	// carries no defaults so an absent routes: key round-trips as absent.
	Routes map[string][]string
}

// configRaw mirrors Config's YAML shape with Tracker left as a raw node so
// UnmarshalYAML can accept either form the wire format allows.
type configRaw struct {
	SchemaVersion int                 `yaml:"schema_version"`
	Name          string              `yaml:"name"`
	TicketFormat  string              `yaml:"ticket_format"`
	Tracker       yaml.Node           `yaml:"tracker"`
	Repos         []Repo              `yaml:"repos"`
	Platform      string              `yaml:"platform"`
	Staircase     []string            `yaml:"staircase,omitempty"`
	Context       map[string]any      `yaml:"context,omitempty"`
	Routes        map[string][]string `yaml:"routes,omitempty"`
}

// UnmarshalYAML decodes project.yaml, accepting the tracker field as either
// a plain scalar ("local", "github", ...) or a map ({command: <path>}).
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
	c.Routes = raw.Routes

	switch raw.Tracker.Kind {
	case 0:
		// tracker not present
	case yaml.ScalarNode:
		c.Tracker = raw.Tracker.Value
	case yaml.MappingNode:
		var m struct {
			Command string `yaml:"command"`
		}
		if err := raw.Tracker.Decode(&m); err != nil {
			return fmt.Errorf("project: decode tracker map: %w", err)
		}
		c.Tracker = "command"
		c.TrackerCmd = m.Command
	default:
		return fmt.Errorf("project: tracker must be a string or a {command: path} map")
	}
	return nil
}

// projectYAML is the on-disk shape written by InitStandalone: unlike
// Config, it always writes Tracker as a plain scalar.
type projectYAML struct {
	SchemaVersion int    `yaml:"schema_version"`
	Name          string `yaml:"name"`
	TicketFormat  string `yaml:"ticket_format"`
	Tracker       string `yaml:"tracker"`
	Repos         []Repo `yaml:"repos"`
	Platform      string `yaml:"platform"`
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

// LoadMachine reads the per-machine project mapping, returning an empty map
// when the file does not exist yet.
func LoadMachine() (map[string]MachineProject, error) {
	path, err := home.MachinePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
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

// SaveMachine writes the per-machine project mapping, creating its parent
// directory if needed.
func SaveMachine(m map[string]MachineProject) error {
	path, err := home.MachinePath()
	if err != nil {
		return err
	}
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
// project.yaml directly in cwd, then a machine-mapping clone that contains
// cwd.
func Resolve(cwd, storeFlag string) (string, Config, error) {
	storePath, err := resolveStorePath(cwd, storeFlag)
	if err != nil {
		return "", Config{}, err
	}
	cfg, err := Load(filepath.Join(storePath, "project.yaml"))
	if err != nil {
		return "", Config{}, err
	}
	return storePath, cfg, nil
}

func resolveStorePath(cwd, storeFlag string) (string, error) {
	if storeFlag != "" {
		return storeFlag, nil
	}
	if _, err := os.Stat(filepath.Join(cwd, "project.yaml")); err == nil {
		return cwd, nil
	}
	machine, err := LoadMachine()
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

// InitStandalone creates a sibling "<repoDir base>-tickets" store next to
// repoDir: a fresh git repo on branch main, project.yaml pointing back at
// repoDir, an empty platform/ dir, and an empty ledger.md. It returns the
// new store's path.
func InitStandalone(repoDir string) (string, error) {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("project: resolve repo dir: %w", err)
	}
	base := filepath.Base(absRepo)
	storeDir := filepath.Join(filepath.Dir(absRepo), base+"-tickets")

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
		Tracker:       "local",
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

	return storeDir, nil
}

// InitProject loads storePath's project.yaml, validates that every clone
// name matches a repo declared there (by Repo.Name), and records the
// mapping in the per-machine store, keyed by the project's name.
func InitProject(storePath string, clones map[string]string) (Config, error) {
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

	machine, err := LoadMachine()
	if err != nil {
		return Config{}, err
	}
	machine[cfg.Name] = MachineProject{Store: absStore, Clones: absClones}
	if err := SaveMachine(machine); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
