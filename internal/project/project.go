// Package project resolves jig's project configuration: the store's
// project.yaml, the per-machine clone mapping, and the two init flows
// (standalone and store+clones).
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// CurrentSchemaVersion is the project.yaml schema_version this jig version
// writes (jig init, internal/fixture) and expects to read. jig store migrate
// is the one command that opens an older store anyway, to rewrite it onto
// this version; CheckSchemaVersion's allowOlder parameter is how it says so.
const CurrentSchemaVersion = 2

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
	FixRounds        *int         `yaml:"fix_rounds"`
	FixRisks         []string     `yaml:"fix_risks"`
	FixSliceFindings *int         `yaml:"fix_slice_findings"`
	ReviewEffort     ReviewEffort `yaml:"review_effort"`
}

// ReviewEffort is gate.review_effort: the gate reviewer's reasoning effort
// by round scope, a "full" round (the first review, or one whose earlier
// reviewed head is not an ancestor) or a "delta" one. A nil field takes its
// default; "" passes no effort, so the CLI's own default applies.
type ReviewEffort struct {
	Full  *string `yaml:"full"`
	Delta *string `yaml:"delta"`
}

// BuilderEffort is builder_effort: a builder's reasoning effort by attempt,
// the first one of a slice or a retry after a failed one. A nil field takes
// its default; "" passes no effort.
type BuilderEffort struct {
	First *string `yaml:"first"`
	Retry *string `yaml:"retry"`
}

// effortLevels are the levels Claude Code's --effort accepts.
var effortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// Effort defaults (ADR 0023): a full review and a builder's retry think
// hard; a delta review and a builder's first attempt think less.
const (
	defaultReviewEffortFull  = "high"
	defaultReviewEffortDelta = "medium"
	defaultBuilderFirst      = "medium"
	defaultBuilderRetry      = "high"
)

// effortOr is v's level, or def when v is absent.
func effortOr(v *string, def string) string {
	if v == nil {
		return def
	}
	return *v
}

// ReviewEffortFor returns the gate reviewer's effort for a round of scope
// ("full" or "delta").
func (c Config) ReviewEffortFor(scope string) string {
	var e ReviewEffort
	if c.Gate != nil {
		e = c.Gate.ReviewEffort
	}
	if scope == "delta" {
		return effortOr(e.Delta, defaultReviewEffortDelta)
	}
	return effortOr(e.Full, defaultReviewEffortFull)
}

// BuilderEffortFor returns a builder's effort for an attempt that follows
// failedAttempts failed attempts of its slice (journal.FailedAttempts, the
// same count that climbs the staircase).
func (c Config) BuilderEffortFor(failedAttempts int) string {
	if failedAttempts > 0 {
		return effortOr(c.BuilderEffort.Retry, defaultBuilderRetry)
	}
	return effortOr(c.BuilderEffort.First, defaultBuilderFirst)
}

// validateEffort refuses a level --effort does not accept; "" (no effort)
// and an absent value pass.
func validateEffort(key string, v *string) error {
	if v == nil || *v == "" {
		return nil
	}
	for _, l := range effortLevels {
		if *v == l {
			return nil
		}
	}
	return fmt.Errorf("project: %s must be one of %s, or empty for none; got %q", key, strings.Join(effortLevels, ", "), *v)
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
	Repos         []Repo
	Platform      string
	Staircase     []string
	Context       map[string]any
	// Gate is the optional gate: block configuring the gate's fix loop and
	// risk floor. An absent block is nil; a block with absent keys gains
	// defaults.
	Gate *GateConfig
	// BuilderEffort is the optional builder_effort: block
	// (BuilderEffortFor applies its defaults).
	BuilderEffort BuilderEffort
	// GitHub is the trackers: list's one github: entry, or nil when the
	// list is empty (or absent, or tracker: local). internal/mirror reads
	// this to decide whether a store's checkpoints sync to GitHub at all.
	GitHub *GitHubTracker
	// Keys is the project's declared area keys, key to its one-line meaning,
	// written as keys:. An id is <key>-<n>; ResolveKey is where every caller
	// that mints or reads a key: resolves one against this.
	Keys map[string]string
}

// GitHubTracker is a project.yaml trackers: list's github: entry: the repo
// its issues live in, and the GitHub Project they are placed on. Both are
// required (brief.md#The trackers entry); a store names its issue home
// explicitly rather than having jig derive it, since a derived home would
// move every issue the day a second repo joined the project, and GitHub
// cannot transfer an issue from a private repo to a public one.
type GitHubTracker struct {
	Repo    string `yaml:"repo"`
	Project string `yaml:"project"`
}

// configRaw mirrors Config's YAML shape, with Tracker, Trackers and Routes
// left as raw nodes so UnmarshalYAML can tell an absent key apart from one
// that is present but empty, and refuse the shapes it no longer accepts.
type configRaw struct {
	SchemaVersion int            `yaml:"schema_version"`
	Name          string         `yaml:"name"`
	TicketFormat  yaml.Node      `yaml:"ticket_format"`
	Keys          yaml.Node      `yaml:"keys"`
	Tracker       yaml.Node      `yaml:"tracker"`
	Trackers      yaml.Node      `yaml:"trackers"`
	Repos         []Repo         `yaml:"repos"`
	Platform      string         `yaml:"platform"`
	Staircase     []string       `yaml:"staircase,omitempty"`
	Context       map[string]any `yaml:"context,omitempty"`
	Routes        yaml.Node      `yaml:"routes"`
	Gate          *GateConfig    `yaml:"gate,omitempty"`
	BuilderEffort BuilderEffort  `yaml:"builder_effort,omitempty"`
}

// UnmarshalYAML decodes project.yaml: trackers: is a list of mirrors (absent
// or empty means none; jig mints every id itself and no mirror shape is
// supported yet, so any entry is refused), ticket_format and tracker: are the
// two keys `jig store migrate` rewrites into keys: and trackers: and so are
// refused once a project.yaml claims schema_version 2 (brief.md#Schema
// version 2) - an older schema_version leaves both unread rather than
// refused, since `jig store migrate` (resolveStoreForProjectAllowingOldSchema)
// must still Load a v1 project.yaml whole, for project.Config.
// CheckSchemaVersion (cmd/jig's resolveStore, the one place every command
// checks it) to be the thing that refuses it for every other command -
// and routes: (which only ever fed publish's now-gone route step) is refused
// outright regardless of schema_version.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	var raw configRaw
	if err := value.Decode(&raw); err != nil {
		return err
	}
	c.SchemaVersion = raw.SchemaVersion
	c.Name = raw.Name
	c.Repos = raw.Repos
	c.Platform = raw.Platform
	c.Staircase = raw.Staircase
	c.Context = raw.Context
	c.Gate = raw.Gate
	c.BuilderEffort = raw.BuilderEffort

	if raw.Routes.Kind != 0 {
		return &axi.Error{
			Msg:  "project.yaml declares routes:, which publish no longer consults",
			Code: "VALIDATION_ERROR",
			Help: []string{"Remove routes: from project.yaml: publish renders the pull request body and the review notes itself"},
		}
	}

	if raw.SchemaVersion >= CurrentSchemaVersion {
		if raw.TicketFormat.Kind != 0 {
			return &axi.Error{
				Msg:  fmt.Sprintf("project.yaml declares schema_version %d and ticket_format, which that schema no longer supports", raw.SchemaVersion),
				Code: "VALIDATION_ERROR",
				Help: []string{
					"Declare keys: instead of ticket_format",
					"`jig store migrate` rewrites an older store's ticket_format into keys:",
				},
			}
		}
		if raw.Tracker.Kind != 0 {
			return &axi.Error{
				Msg:  fmt.Sprintf("project.yaml declares schema_version %d and tracker:, which that schema no longer supports", raw.SchemaVersion),
				Code: "VALIDATION_ERROR",
				Help: []string{
					"Declare trackers: instead of tracker: (e.g. trackers: [])",
					"`jig store migrate` rewrites an older store's tracker: into trackers:",
				},
			}
		}
	}

	if err := c.loadKeys(raw); err != nil {
		return err
	}

	if raw.Trackers.Kind != 0 {
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
		if len(entries) > 1 {
			return &axi.Error{
				Msg:  "project.yaml's trackers: declares more than one entry",
				Code: "VALIDATION_ERROR",
				Help: []string{"At most one github: entry is supported"},
			}
		}
		if len(entries) == 1 {
			gh, err := decodeGitHubTrackerEntry(entries[0])
			if err != nil {
				return err
			}
			c.GitHub = gh
		}
	}

	if err := c.validateGateConfig(); err != nil {
		return err
	}
	if err := validateEffort("builder_effort.first", c.BuilderEffort.First); err != nil {
		return err
	}
	return validateEffort("builder_effort.retry", c.BuilderEffort.Retry)
}

// decodeGitHubTrackerEntry decodes one trackers: list entry, refusing
// anything but a mapping with exactly one key, "github", whose value is
// itself a mapping with only repo: and project:, both non-empty
// (brief.md#The trackers entry: "a second, an unknown key, or a malformed
// value is refused when the config loads").
func decodeGitHubTrackerEntry(node yaml.Node) (*GitHubTracker, error) {
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return nil, malformedTrackerEntryError("each trackers: entry must be a mapping with exactly one key")
	}
	key, valueNode := node.Content[0].Value, node.Content[1]
	if key != "github" {
		return nil, malformedTrackerEntryError(fmt.Sprintf("trackers: entry key %q is not supported; only github is", key))
	}
	if valueNode.Kind != yaml.MappingNode {
		return nil, malformedTrackerEntryError("trackers: github: entry must be a mapping")
	}
	known := map[string]bool{"repo": true, "project": true}
	for i := 0; i < len(valueNode.Content); i += 2 {
		if k := valueNode.Content[i].Value; !known[k] {
			return nil, malformedTrackerEntryError(fmt.Sprintf("trackers: github: entry has unknown key %q", k))
		}
	}
	var gh GitHubTracker
	if err := valueNode.Decode(&gh); err != nil {
		return nil, malformedTrackerEntryError(fmt.Sprintf("decode trackers: github: entry: %v", err))
	}
	if gh.Repo == "" || gh.Project == "" {
		return nil, malformedTrackerEntryError("trackers: github: entry requires both repo: and project:")
	}
	return &gh, nil
}

// malformedTrackerEntryError is the VALIDATION_ERROR decodeGitHubTrackerEntry
// refuses with, why naming what was wrong.
func malformedTrackerEntryError(why string) error {
	return &axi.Error{
		Msg:  "project.yaml's trackers: entry is invalid: " + why,
		Code: "VALIDATION_ERROR",
		Help: []string{"trackers: takes at most one entry: github:, with repo: <owner>/<name> and project: <url>"},
	}
}

// keyRE matches a key: entry's own key: 2 to 10 uppercase ASCII letters and
// digits, starting with a letter. Keys are uppercase so that a filesystem
// that ignores case, the Windows and macOS default, never confuses STORE-1
// with store-1.
var keyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// loadKeys resolves c.Keys from raw's keys:, refusing the shapes
// brief.md#Keys in project.yaml names. An absent keys: leaves c.Keys nil.
func (c *Config) loadKeys(raw configRaw) error {
	if raw.Keys.Kind == 0 {
		return nil
	}
	var keys map[string]string
	if err := raw.Keys.Decode(&keys); err != nil {
		return fmt.Errorf("project: decode keys: %w", err)
	}
	for key, meaning := range keys {
		if err := validateKey(key, meaning); err != nil {
			return err
		}
	}
	c.Keys = keys
	return nil
}

// validateKey refuses a keys: entry whose key breaks the key rule or whose
// meaning is empty, naming the key either way.
func validateKey(key, meaning string) error {
	if !keyRE.MatchString(key) {
		return &axi.Error{
			Msg:  fmt.Sprintf("project.yaml's keys: declares %q, which is not 2 to 10 uppercase letters or digits starting with a letter", key),
			Code: "VALIDATION_ERROR",
			Help: []string{"Fix the key in project.yaml's keys:"},
		}
	}
	if meaning == "" {
		return &axi.Error{
			Msg:  fmt.Sprintf("project.yaml's keys: entry %q has no meaning", key),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Add a one-line meaning for key %q in project.yaml's keys:", key)},
		}
	}
	return nil
}

// deriveProjectKey derives the key jig init (and the test fixture) declare
// for a project named name, absent an explicit --key: name's letters and
// digits, uppercased, at most 10 - "jig" becomes "JIG", meaning "everything
// in jig". A name with fewer than 2 such characters derives no valid key
// (keyRE's own 2-character minimum), refused so the caller passes --key
// instead of minting under an empty or one-letter key.
func deriveProjectKey(name string) (string, error) {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
		if b.Len() == 10 {
			break
		}
	}
	key := b.String()
	if !keyRE.MatchString(key) {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("project name %q has no key jig can derive from it", name),
			Code: "VALIDATION_ERROR",
			Help: []string{"Pass --key <KEY> (2 to 10 uppercase letters or digits, starting with a letter)"},
		}
	}
	return key, nil
}

// ResolveKey resolves the key to mint under: key itself when non-empty, or
// the project's one declared key when it declares exactly one and key is
// empty. It refuses - before anything is minted - an empty key when the
// project declares more than one (ambiguous) and any key the project does
// not declare, listing the declared keys and their meanings and pointing at
// project.yaml's keys: to add one.
func (c Config) ResolveKey(key string) (string, error) {
	if key == "" {
		if len(c.Keys) == 1 {
			for k := range c.Keys {
				return k, nil
			}
		}
		return "", c.undeclaredKeyError(key)
	}
	if _, ok := c.Keys[key]; !ok {
		return "", c.undeclaredKeyError(key)
	}
	return key, nil
}

// undeclaredKeyError is ResolveKey's refusal, naming key when one was given
// (empty when none was and the project declares more than one), with help
// listing every declared key and its meaning.
func (c Config) undeclaredKeyError(key string) error {
	var msg string
	if key == "" {
		msg = "project.yaml declares more than one key: --key (or a key: on the chart entry) is required"
	} else {
		msg = fmt.Sprintf("project.yaml does not declare key %q", key)
	}
	help := []string{"Declared keys:"}
	names := make([]string, 0, len(c.Keys))
	for k := range c.Keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		help = append(help, fmt.Sprintf("  %s: %s", k, c.Keys[k]))
	}
	help = append(help, "Add the key to project.yaml's keys: to mint under it")
	return &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: help}
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
	if err := validateEffort("gate.review_effort.full", c.Gate.ReviewEffort.Full); err != nil {
		return err
	}
	return validateEffort("gate.review_effort.delta", c.Gate.ReviewEffort.Delta)
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

// projectYAML is the on-disk shape written by InitStandalone, at
// CurrentSchemaVersion: Trackers always marshals as "trackers: []" (a nil
// slice, no omitempty), and no tracker: or ticket_format key is ever
// written.
type projectYAML struct {
	SchemaVersion int               `yaml:"schema_version"`
	Name          string            `yaml:"name"`
	Keys          map[string]string `yaml:"keys"`
	Trackers      []string          `yaml:"trackers"`
	Repos         []Repo            `yaml:"repos"`
	Platform      string            `yaml:"platform"`
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

// CheckSchemaVersion refuses a schema_version CurrentSchemaVersion cannot
// work with: newer, always (jig itself would have to be upgraded to
// understand it), and older than CurrentSchemaVersion unless allowOlder.
// Every command resolving a store (cmd/jig's resolveStore, the one place
// every command loads project.yaml) calls this with allowOlder false; `jig
// store migrate` is the one command that passes true, since migrating a v1
// store to CurrentSchemaVersion is the whole point of opening it. `jig help`
// and `jig version` never resolve a store at all, so neither check ever runs
// for them.
func (c Config) CheckSchemaVersion(allowOlder bool) error {
	if c.SchemaVersion > CurrentSchemaVersion {
		return &axi.Error{
			Msg:  fmt.Sprintf("project.yaml's schema_version is %d, newer than this jig version understands (%d)", c.SchemaVersion, CurrentSchemaVersion),
			Code: "VALIDATION_ERROR",
			Help: []string{"Upgrade jig to a version that supports this store's schema_version"},
		}
	}
	if c.SchemaVersion < CurrentSchemaVersion && !allowOlder {
		return &axi.Error{
			Msg:  fmt.Sprintf("project.yaml's schema_version is %d; this store has not been migrated to %d yet", c.SchemaVersion, CurrentSchemaVersion),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig store migrate` to migrate this store to the current layout"},
		}
	}
	return nil
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
// repoDir, an empty platform/ dir, and an empty ledger.md. key is the area
// key project.yaml's keys: declares, meaning "everything in <name>"; ""
// derives it from repoDir's base name (deriveProjectKey), the --key flag's
// default. It returns the new store's path. Run against an existing store,
// it resets project.yaml and ledger.md.
func InitStandalone(repoDir, key string) (string, error) {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("project: resolve repo dir: %w", err)
	}
	base := filepath.Base(absRepo)
	storeDir := siblingStoreDir(absRepo)

	if key == "" {
		key, err = deriveProjectKey(base)
		if err != nil {
			return "", err
		}
	} else if !keyRE.MatchString(key) {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("--key %q is not 2 to 10 uppercase letters or digits starting with a letter", key),
			Code: "VALIDATION_ERROR",
		}
	}

	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return "", fmt.Errorf("project: create store dir: %w", err)
	}
	if _, err := gitx.Run(storeDir, "init", "-b", "main"); err != nil {
		return "", fmt.Errorf("project: git init: %w", err)
	}

	py := projectYAML{
		SchemaVersion: CurrentSchemaVersion,
		Name:          base,
		Keys:          map[string]string{key: fmt.Sprintf("everything in %s", base)},
		Trackers:      []string{},
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
