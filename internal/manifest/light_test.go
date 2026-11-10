package manifest

import (
	"path/filepath"
	"reflect"
	"testing"
)

var jigLight = Light{Paths: []string{"*.md", ".github/*.md", "docs/**/*.md"}, Oracle: "go test ./lint"}

// TestLightMatches: a delta is light when it has a file and every file matches
// a pattern; "*" stops at "/", "**" crosses it.
func TestLightMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		{"root markdown", []string{"README.md"}, true},
		{".github markdown", []string{".github/CONTRIBUTING.md"}, true},
		{"docs markdown", []string{"docs/readme.md"}, true},
		{"docs nested markdown", []string{"docs/adr/0001-x.md", "docs/notes/example.md"}, true},
		{"skills markdown", []string{"skills/build/SKILL.md"}, false},
		{"testdata markdown", []string{"internal/x/testdata/in.md"}, false},
		{"nested markdown outside docs", []string{"cmd/jig/NOTES.md"}, false},
		{"non-markdown in docs", []string{"docs/adr/diagram.png"}, false},
		{"go file", []string{"main.go"}, false},
		{"one non-light file among light ones", []string{"README.md", "docs/a.md", "go.mod"}, false},
		{"deleted markdown is just a path", []string{"CONTEXT.md"}, true},
		{"a .github subdirectory is not crossed by *", []string{".github/ISSUE/a.md"}, false},
		{"no file", nil, false},
	}
	for _, c := range cases {
		if got := jigLight.Matches(c.files); got != c.want {
			t.Errorf("%s: Matches(%v) = %v, want %v", c.name, c.files, got, c.want)
		}
	}
	if (Light{}).Matches([]string{"README.md"}) {
		t.Error("a manifest with no light rule matched a delta")
	}
}

// TestResolveLight: the light key resolves from jig.yaml, both keys are
// required, and a bad pattern is refused.
func TestResolveLight(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), "light:\n  paths: [\"*.md\", \"docs/**/*.md\"]\n  oracle: go test ./lint\n")
	m, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !m.Light.Declared() || m.Light.Oracle != "go test ./lint" || !reflect.DeepEqual(m.Light.Paths, []string{"*.md", "docs/**/*.md"}) {
		t.Errorf("Light = %+v", m.Light)
	}

	none := t.TempDir()
	if m, err := Resolve(none); err != nil || m.Light.Declared() {
		t.Errorf("no jig.yaml: Light = %+v, err %v, want none declared", m.Light, err)
	}

	for name, decl := range map[string]string{
		"paths without oracle": "light:\n  paths: [\"*.md\"]\n",
		"oracle without paths": "light:\n  oracle: go test ./lint\n",
		"absolute path":        "light:\n  paths: [\"/x.md\"]\n  oracle: c\n",
		"escaping path":        "light:\n  paths: [\"../x.md\"]\n  oracle: c\n",
		"bad pattern":          "light:\n  paths: [\"[x.md\"]\n  oracle: c\n",
	} {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, ".claude", "jig.yaml"), decl)
		if _, err := Resolve(d); err == nil {
			t.Errorf("%s: Resolve accepted it", name)
		}
	}
}
