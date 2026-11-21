package project

import (
	"fmt"
	"path/filepath"
	"testing"
)

func loadTrackerYAML(t *testing.T, entry string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "project.yaml")
	writeFile(t, p, fmt.Sprintf(`
schema_version: 2
name: demo
keys:
  JIG: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/orgs/example/projects/1
%s
repos: []
platform: platform/
`, entry))
	return Load(p)
}

// TestTrackerTasksDecodesTheSetting: tasks: true and false are read, and an
// absent key means false.
func TestTrackerTasksDecodesTheSetting(t *testing.T) {
	for _, c := range []struct {
		entry string
		want  bool
	}{{"", false}, {"      tasks: true", true}, {"      tasks: false", false}} {
		cfg, err := loadTrackerYAML(t, c.entry)
		if err != nil {
			t.Fatalf("%q: %v", c.entry, err)
		}
		if cfg.GitHub == nil || cfg.GitHub.Tasks != c.want {
			t.Errorf("%q: Tasks = %+v, want %v", c.entry, cfg.GitHub, c.want)
		}
	}
}

// TestTrackerTasksRefusesANonBoolean: any other value is a malformed entry.
func TestTrackerTasksRefusesANonBoolean(t *testing.T) {
	for _, v := range []string{`"true"`, "yes please", "1", "[true]", "null", "{}"} {
		_, err := loadTrackerYAML(t, "      tasks: "+v)
		if err == nil {
			t.Errorf("tasks: %s loaded; want a refusal", v)
			continue
		}
		wantValidationError(t, err)
	}
}
