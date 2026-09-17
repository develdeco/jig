// Package manifest resolves a repo's build surface: its workspaces, its
// oracle commands, and its environment classes, by overlaying detected
// defaults with a declared .claude/jig.yaml.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
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
