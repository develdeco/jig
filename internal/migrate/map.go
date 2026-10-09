// Package migrate implements `jig store migrate`: the one-time move of a
// v1 store (tickets at the store root, one ticket_format id) onto layout v2
// (tickets/<id>/, schema_version 2, every id carrying an area key), per the
// operator's rename map (brief.md#The rename map, brief.md#The migration).
package migrate

import (
	"fmt"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// Map is the operator's rename map file: keys: becomes the new store's
// project.yaml keys:, and tickets: assigns every existing ticket its new
// key.
type Map struct {
	// Keys is the new area keys, key to its one-line meaning.
	Keys map[string]string
	// Tickets is every existing ticket's old id to the key it moves under.
	Tickets map[string]string
}

// mapFile is the map file's wire shape.
type mapFile struct {
	Keys    map[string]string `yaml:"keys"`
	Tickets map[string]string `yaml:"tickets"`
}

// keyRE matches a map key: entry's own key, the same rule
// project.yaml's keys: applies (internal/project's keyRE): 2 to 10
// uppercase ASCII letters and digits, starting with a letter.
var keyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// LoadMap reads and parses the rename map file at path, refusing a key that
// breaks the key rule or carries no meaning. It does not check the map
// against any store; BuildPlan does that once a store is open.
func LoadMap(path string) (Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Map{}, &axi.Error{
			Msg:  fmt.Sprintf("read the rename map %s: %v", path, err),
			Code: "VALIDATION_ERROR",
		}
	}
	var mf mapFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return Map{}, &axi.Error{
			Msg:  fmt.Sprintf("parse the rename map %s: %v", path, err),
			Code: "VALIDATION_ERROR",
		}
	}
	for key, meaning := range mf.Keys {
		if !keyRE.MatchString(key) {
			return Map{}, &axi.Error{
				Msg:  fmt.Sprintf("the rename map's keys: declares %q, which is not 2 to 10 uppercase letters or digits starting with a letter", key),
				Code: "VALIDATION_ERROR",
				Help: []string{"Fix the key in the rename map's keys:"},
			}
		}
		if meaning == "" {
			return Map{}, &axi.Error{
				Msg:  fmt.Sprintf("the rename map's keys: entry %q has no meaning", key),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Add a one-line meaning for key %q in the rename map's keys:", key)},
			}
		}
	}
	return Map{Keys: mf.Keys, Tickets: mf.Tickets}, nil
}

// sortedKeys returns m's keys, sorted.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
