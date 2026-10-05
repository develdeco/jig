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
	"strings"

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
