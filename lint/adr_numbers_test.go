package lint

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"testing"
	"testing/fstest"
)

// adrNumberRE is the four digits an ADR file name leads with.
var adrNumberRE = regexp.MustCompile(`^[0-9]{4}`)

// adrNumberProblems reports each ADR number two files of dir share. Two
// decisions written side by side can pick the same next number; nothing else
// would notice until a link to one of them pointed at the wrong decision. A
// file without leading digits is not an ADR and is ignored.
func adrNumberProblems(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var problems []string
	for _, e := range entries {
		n := adrNumberRE.FindString(e.Name())
		if n == "" {
			continue
		}
		if first, ok := seen[n]; ok {
			problems = append(problems, fmt.Sprintf("ADR number %s is used by both %s and %s", n, first, e.Name()))
			continue
		}
		seen[n] = e.Name()
	}
	return problems, nil
}

func TestADRNumbersAreUnique(t *testing.T) {
	t.Run("a number two files share fails, naming both", func(t *testing.T) {
		fsys := fstest.MapFS{
			"adr/0007-first.md":  {Data: []byte("x")},
			"adr/0007-second.md": {Data: []byte("x")},
			"adr/0008-other.md":  {Data: []byte("x")},
			"adr/README.md":      {Data: []byte("x")},
		}
		problems, err := adrNumberProblems(fsys, "adr")
		if err != nil {
			t.Fatal(err)
		}
		want := "ADR number 0007 is used by both 0007-first.md and 0007-second.md"
		if len(problems) != 1 || problems[0] != want {
			t.Errorf("problems = %q, want [%q]", problems, want)
		}
	})
	t.Run("distinct numbers and files without one are clean", func(t *testing.T) {
		fsys := fstest.MapFS{
			"adr/0001-a.md": {Data: []byte("x")},
			"adr/0002-b.md": {Data: []byte("x")},
			"adr/notes.md":  {Data: []byte("x")},
			"adr/draft.md":  {Data: []byte("x")},
		}
		if problems, err := adrNumberProblems(fsys, "adr"); err != nil || len(problems) != 0 {
			t.Errorf("problems = %q, %v, want none", problems, err)
		}
	})
	t.Run("the real tree", func(t *testing.T) {
		problems, err := adrNumberProblems(os.DirFS(repoRoot(t)), "docs/adr")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Error(p)
		}
	})
}
