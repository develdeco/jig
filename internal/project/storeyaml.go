package project

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// storeYAML is the on-disk shape of a v3 store's store.yaml, as `jig init`
// writes it. A store keeps what says how its tickets are written: the schema
// version, the area keys, the trackers its checkpoints mirror to, the
// platform folder, and the projects it serves. Everything that describes a
// project (its name, repos, harness expectations, branch rule, budget) is the
// project's jig.yaml. Trackers always marshals as "trackers: []" (a nil slice,
// no omitempty). The migration from a v2 store edits the existing document
// instead (migrate), so a project.yaml's other keys (gate:, context:,
// staircase:, builder_effort:) and its comments come along.
type storeYAML struct {
	SchemaVersion int               `yaml:"schema_version"`
	Keys          map[string]string `yaml:"keys"`
	Trackers      []string          `yaml:"trackers"`
	Platform      string            `yaml:"platform"`
	Projects      []string          `yaml:"projects"`
}

// NewStoreYAML returns the store.yaml of a new v3 store that serves project
// name, declaring the one area key, meaning "everything in <name>".
func NewStoreYAML(name, key string) ([]byte, error) {
	data, err := yaml.Marshal(storeYAML{
		SchemaVersion: CurrentSchemaVersion,
		Keys:          map[string]string{key: fmt.Sprintf("everything in %s", name)},
		Trackers:      []string{},
		Platform:      "platform/",
		Projects:      []string{name},
	})
	if err != nil {
		return nil, fmt.Errorf("project: marshal %s: %w", StoreFile, err)
	}
	return data, nil
}
