package setup

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/develdeco/jig/skills"
)

// TestKeepsTheStubWithinTheSkillLineCap: the stub jig attach writes, listing
// every skill the binary embeds, stays within the 100-line skill cap.
func TestKeepsTheStubWithinTheSkillLineCap(t *testing.T) {
	t.Parallel()
	dirs, err := fs.ReadDir(skills.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var entries []StubEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		body, err := fs.ReadFile(skills.FS, d.Name()+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, StubEntry{Name: d.Name(), Summary: SkillSummary(body)})
	}
	if len(entries) == 0 {
		t.Fatal("no embedded skills")
	}
	if n := strings.Count(string(StubBody(entries)), "\n"); n > 100 {
		t.Errorf("the stub is %d lines, want at most 100", n)
	}
}
