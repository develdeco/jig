package migrate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRewriteProjectYAMLTrackerLocalBecomesEmptyTrackers(t *testing.T) {
	t.Parallel()
	input := `schema_version: 1
name: demo
ticket_format: "T-{n}"
tracker: local
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
`
	out, err := RewriteProjectYAML([]byte(input), map[string]string{"T": "everything in demo"})
	if err != nil {
		t.Fatalf("RewriteProjectYAML: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "ticket_format") {
		t.Errorf("output still has ticket_format:\n%s", s)
	}
	if strings.Contains(s, "tracker:") && !strings.Contains(s, "trackers:") {
		t.Errorf("output still has tracker: (not renamed):\n%s", s)
	}

	var got struct {
		SchemaVersion int               `yaml:"schema_version"`
		Name          string            `yaml:"name"`
		Keys          map[string]string `yaml:"keys"`
		Trackers      []string          `yaml:"trackers"`
		Platform      string            `yaml:"platform"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse rewritten project.yaml: %v\n%s", err, s)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", got.SchemaVersion)
	}
	if got.Name != "demo" || got.Platform != "platform/" {
		t.Errorf("name/platform changed unexpectedly: %+v", got)
	}
	if got.Keys["T"] != "everything in demo" {
		t.Errorf("Keys = %+v, want T: everything in demo", got.Keys)
	}
	if len(got.Trackers) != 0 {
		t.Errorf("Trackers = %+v, want empty", got.Trackers)
	}
}

// TestRewriteProjectYAMLKeepsAnExistingTrackersList pins "an existing
// trackers: is kept" (brief.md#The migration step 6): a project.yaml that
// already carries a trackers: list (no tracker: key) is left untouched by
// the tracker rewrite.
func TestRewriteProjectYAMLKeepsAnExistingTrackersList(t *testing.T) {
	t.Parallel()
	input := `schema_version: 1
name: demo
ticket_format: "T-{n}"
trackers:
  - github:
      repo: org/demo
      project: https://example.invalid/projects/1
repos: []
platform: platform/
`
	out, err := RewriteProjectYAML([]byte(input), map[string]string{"T": "everything in demo"})
	if err != nil {
		t.Fatalf("RewriteProjectYAML: %v", err)
	}
	var got struct {
		Trackers []map[string]map[string]string `yaml:"trackers"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(got.Trackers) != 1 || got.Trackers[0]["github"]["repo"] != "org/demo" {
		t.Errorf("Trackers = %+v, want the github entry kept", got.Trackers)
	}
}

// TestRewriteProjectYAMLKeepsEveryOtherKey pins "every other key kept as it
// is" (brief.md#The migration step 6): gate: and staircase: round-trip
// unchanged.
func TestRewriteProjectYAMLKeepsEveryOtherKey(t *testing.T) {
	t.Parallel()
	input := `schema_version: 1
name: demo
ticket_format: "T-{n}"
tracker: local
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
staircase: [sonnet, opus]
gate:
  fix_rounds: 5
`
	out, err := RewriteProjectYAML([]byte(input), map[string]string{"T": "everything in demo"})
	if err != nil {
		t.Fatalf("RewriteProjectYAML: %v", err)
	}
	var got struct {
		Staircase []string `yaml:"staircase"`
		Gate      struct {
			FixRounds int `yaml:"fix_rounds"`
		} `yaml:"gate"`
		Repos []struct {
			Remote string `yaml:"remote"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(got.Staircase) != 2 || got.Staircase[0] != "sonnet" {
		t.Errorf("Staircase = %+v, want [sonnet opus]", got.Staircase)
	}
	if got.Gate.FixRounds != 5 {
		t.Errorf("gate.fix_rounds = %d, want 5", got.Gate.FixRounds)
	}
	if len(got.Repos) != 1 || got.Repos[0].Remote != "https://example.invalid/org/demo.git" {
		t.Errorf("Repos = %+v, unchanged expected", got.Repos)
	}
}
