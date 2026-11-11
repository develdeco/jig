// Package manifest resolves a repo's build surface: its workspaces, its
// oracle commands, and its environment classes, by overlaying detected
// defaults with a declared .claude/jig.yaml.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Workspace is one buildable unit within a repo.
type Workspace struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

// EnvClass is a named, scriptable environment lifecycle: bring it up, check
// it, tear it down. Unavailable names the policy when Up cannot run at all
// ("defer-ci" to skip and let CI cover it later, or "" to pause).
type EnvClass struct {
	Up          string
	Check       string
	Down        string
	Docs        string
	Unavailable string `yaml:"unavailable"`
}

// Manifest is a repo's resolved build surface.
type Manifest struct {
	Workspaces []Workspace         `yaml:"workspaces"`
	Oracles    map[string]string   `yaml:"oracles"`
	Envs       map[string]EnvClass `yaml:"envs"`
	Invariants []string            `yaml:"invariants"`
	// Timeout is the longest one oracle run of this repo may take, declared
	// as a duration with a unit ("45m", "1h30m"); zero means it declared none
	// (OracleLimit gives the default). It is a wedge guard, never a cost
	// cap: a run past it is killed and counts as failed.
	Timeout time.Duration `yaml:"timeout"`
	// Light declares the delta that reuses a whole-suite pass; the zero value
	// declares none, so a repo that says nothing never reuses.
	Light Light `yaml:"light,omitempty"`
	// Heavy names the oracles that take the project's suite slot when a slice
	// or the gate runs them by that name; a literal command takes none, and a
	// manifest that names none takes no slot.
	Heavy []string `yaml:"heavy,omitempty"`
}

// IsHeavy reports whether the oracle named name takes the suite slot.
func (m Manifest) IsHeavy(name string) bool {
	return name != "" && slices.Contains(m.Heavy, name)
}

// Light is the rule for a delta that cannot change what the slice's oracle
// proves: Paths are the patterns a changed file must match (in a pattern "*"
// stops at "/" and a "**" segment crosses any number of directories, so
// "docs/**/*.md" covers "docs/x.md" and "docs/adr/y.md"), and Oracle the
// command that runs in the oracle's place on such a delta. jig holds only the
// mechanism; which files are safe is a fact of the repo whose tests read them.
type Light struct {
	Paths  []string `yaml:"paths"`
	Oracle string   `yaml:"oracle"`
}

// Declared reports whether the manifest declared a light rule.
func (l Light) Declared() bool { return len(l.Paths) > 0 && strings.TrimSpace(l.Oracle) != "" }

// Matches reports whether files, repo-relative with forward slashes, are a
// light delta: there is at least one, and every one matches a pattern. A
// delta with no file is not light (nothing to say it is safe), and neither is
// any delta when no light rule is declared.
func (l Light) Matches(files []string) bool {
	if !l.Declared() || len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !l.matchesFile(f) {
			return false
		}
	}
	return true
}

func (l Light) matchesFile(file string) bool {
	segs := strings.Split(file, "/")
	for _, p := range l.Paths {
		if matchSegments(strings.Split(p, "/"), segs) {
			return true
		}
	}
	return false
}

// matchSegments matches a pattern's segments against a path's: a "**"
// segment takes zero or more path segments, any other is a path.Match
// pattern for exactly one.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], segs[0]); !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}

// validateLight checks a declared light rule: both keys, and patterns that
// are relative, forward-slashed, inside the repo and valid for path.Match.
func validateLight(l Light) error {
	if len(l.Paths) == 0 || strings.TrimSpace(l.Oracle) == "" {
		return fmt.Errorf("light needs both paths and oracle")
	}
	for _, p := range l.Paths {
		if p == "" {
			return fmt.Errorf("light path is empty")
		}
		if strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
			return fmt.Errorf("light path %q is absolute", p)
		}
		if strings.Contains(p, "\\") {
			return fmt.Errorf("light path %q contains backslashes; use forward slashes", p)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return fmt.Errorf("light path %q escapes the repo", p)
			}
			if _, err := path.Match(seg, "x"); err != nil {
				return fmt.Errorf("light path %q is not a valid pattern: %w", p, err)
			}
		}
	}
	return nil
}

// DefaultOracleLimit is how long one oracle run may take when the manifest
// declares no timeout: the same bound a headless session's shell gives one
// command (ADR 0018), so an oracle that fits a builder's shell fits here too.
const DefaultOracleLimit = 30 * time.Minute

// OracleLimit is the longest one oracle run may take: the declared timeout,
// or DefaultOracleLimit when the manifest declares none.
func (m Manifest) OracleLimit() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return DefaultOracleLimit
}

// declaredPath is where a repo may override detection.
const declaredPath = ".claude/jig.yaml"

// Resolve builds repoDir's manifest: detection (go.mod → a "test" oracle;
// package.json scripts → one oracle per script) overlaid by a declared
// .claude/jig.yaml, where declared workspaces replace detected ones,
// declared oracles merge over detected ones (declared wins on the same
// name), and envs come only from the declaration.
func Resolve(repoDir string) (Manifest, error) {
	m := Manifest{
		Workspaces: []Workspace{{ID: "root", Path: "."}},
		Oracles:    map[string]string{},
		Envs:       map[string]EnvClass{},
	}

	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		m.Oracles["test"] = "go test ./..."
	}

	if data, err := os.ReadFile(filepath.Join(repoDir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if jerr := json.Unmarshal(data, &pkg); jerr != nil {
			return Manifest{}, fmt.Errorf("manifest: parse package.json: %w", jerr)
		}
		for name := range pkg.Scripts {
			m.Oracles[name] = "npm run " + name
		}
	}

	declFile := filepath.Join(repoDir, filepath.FromSlash(declaredPath))
	data, err := os.ReadFile(declFile)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return Manifest{}, fmt.Errorf("manifest: read %s: %w", declFile, err)
	}

	var declared Manifest
	if err := yaml.Unmarshal(data, &declared); err != nil {
		return Manifest{}, fmt.Errorf("manifest: parse %s: %w", declFile, err)
	}

	if len(declared.Workspaces) > 0 {
		m.Workspaces = declared.Workspaces
	}
	for name, cmd := range declared.Oracles {
		m.Oracles[name] = cmd
	}
	if len(declared.Envs) > 0 {
		m.Envs = declared.Envs
	}
	if len(declared.Invariants) > 0 {
		if err := validateInvariants(declared.Invariants); err != nil {
			return Manifest{}, fmt.Errorf("manifest: validate invariants: %w", err)
		}
		m.Invariants = declared.Invariants
	}
	if declared.Timeout < 0 {
		return Manifest{}, fmt.Errorf("manifest: timeout %s is negative", declared.Timeout)
	}
	m.Timeout = declared.Timeout
	for _, name := range declared.Heavy {
		if strings.TrimSpace(name) == "" {
			return Manifest{}, fmt.Errorf("manifest: heavy names an empty oracle in %s", declFile)
		}
	}
	m.Heavy = declared.Heavy
	if len(declared.Light.Paths) > 0 || declared.Light.Oracle != "" {
		if err := validateLight(declared.Light); err != nil {
			return Manifest{}, fmt.Errorf("manifest: validate light: %w", err)
		}
		m.Light = declared.Light
	}

	return m, nil
}

// Workspace looks up a workspace by id.
func (m Manifest) Workspace(id string) (Workspace, bool) {
	for _, w := range m.Workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return Workspace{}, false
}

// WorkspaceIn looks up the workspace a slice names, in the form the slice
// writes it: "<repo>/<id>" in a project of several repos, the bare id in a
// project of one. repo is the name of the repo m belongs to. An id m declares
// exactly as written wins, so a bare id that holds a slash still finds its
// workspace; else the "<repo>/" in front of the written id is taken off. A
// workspace written for another repo is not found.
func (m Manifest) WorkspaceIn(repo, written string) (Workspace, bool) {
	if w, ok := m.Workspace(written); ok {
		return w, true
	}
	if rest, ok := strings.CutPrefix(written, repo+"/"); ok && repo != "" && rest != "" {
		return m.Workspace(rest)
	}
	return Workspace{}, false
}

// OracleCmd resolves a slice's oracle field to a runnable command: when it
// names a manifest oracle, that oracle's template with {path} substituted
// for ws.Path; otherwise oracleField is used verbatim as a literal command.
func (m Manifest) OracleCmd(oracleField string, ws Workspace) string {
	if tmpl, ok := m.Oracles[oracleField]; ok {
		return strings.ReplaceAll(tmpl, "{path}", ws.Path)
	}
	return oracleField
}

// MatchesInvariant reports whether filePath matches any declared invariant.
// filePath is a repo-relative path with forward slashes.
func (m Manifest) MatchesInvariant(filePath string) bool {
	for _, inv := range m.Invariants {
		pattern, isDir := normalizeInvariant(inv)
		if isDir {
			// The repo root as a directory entry ("." or "./") covers every
			// path in the repo; any other covers its own subtree.
			if pattern == "." || strings.HasPrefix(filePath, pattern+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern, filePath); ok {
			return true
		}
	}
	return false
}

// normalizeInvariant reduces a declared invariant to the form it is matched
// in: its path.Clean'ed form, plus whether it named a directory (an entry
// ending in "/"). Matching the cleaned form is what makes matching agree with
// validateInvariants, which judges the same cleaned form: git reports a
// changed file's path cleaned, so an entry matched as written - say
// "./migrations/*.sql", which validates fine - could never match any real
// path, and would floor nothing while looking like it declared something.
func normalizeInvariant(inv string) (pattern string, isDir bool) {
	isDir = strings.HasSuffix(inv, "/")
	if isDir {
		inv = strings.TrimSuffix(inv, "/")
	}
	return path.Clean(inv), isDir
}

// validateInvariants checks that each invariant is valid: not absolute, not
// escaping the repo (no ..), and if a glob pattern (not ending in /), it is
// valid for path.Match. Each entry is judged in the same cleaned form
// MatchesInvariant matches it in (normalizeInvariant), so no entry can pass
// validation only to match nothing at dispatch time.
func validateInvariants(invs []string) error {
	for _, inv := range invs {
		if len(inv) == 0 {
			return fmt.Errorf("invariant is empty")
		}
		if strings.HasPrefix(inv, "/") || (len(inv) > 1 && inv[1] == ':') {
			return fmt.Errorf("invariant %q is absolute", inv)
		}
		if strings.Contains(inv, "\\") {
			return fmt.Errorf("invariant %q contains backslashes; use forward slashes", inv)
		}
		pattern, isDir := normalizeInvariant(inv)
		if strings.HasPrefix(pattern, "..") || strings.Contains(pattern, "/..") {
			return fmt.Errorf("invariant %q escapes the repo", inv)
		}
		if !isDir {
			if _, err := path.Match(pattern, "test"); err != nil {
				return fmt.Errorf("invariant %q is not a valid path.Match pattern: %w", inv, err)
			}
		}
	}
	return nil
}
