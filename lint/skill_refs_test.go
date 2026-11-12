package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// refLinkRE matches a "jig skill <name> <ref>" pointer.
var refLinkRE = regexp.MustCompile("`jig skill ([a-z0-9][a-z0-9-]*) ([a-z][a-z0-9-]*)`")

// skillTreeProblems checks a skills tree (<name>/SKILL.md and
// <name>/references/<ref>.md): every file is within the line and byte caps,
// every reference is linked from its core, a link names a reference that
// exists, and a reference links no other reference.
func skillTreeProblems(fsys fs.FS) []string {
	var problems []string
	cores, _ := fs.Glob(fsys, "*/SKILL.md")
	refs := map[string]bool{} // "<name>/<ref>"
	files, _ := fs.Glob(fsys, "*/references/*.md")
	for _, f := range files {
		refs[path.Dir(path.Dir(f))+"/"+strings.TrimSuffix(path.Base(f), ".md")] = true
	}
	capped := append(append([]string{}, cores...), files...)
	text := map[string]string{}
	for _, f := range capped {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		s := string(normalizeEOL(data))
		text[f] = s
		if n := strings.Count(s, "\n"); n > 100 {
			problems = append(problems, fmt.Sprintf("%s: %d lines, budget is <=100", f, n))
		}
		if len(s) > 6000 {
			problems = append(problems, fmt.Sprintf("%s: %d bytes, budget is <=6000", f, len(s)))
		}
	}
	for _, f := range files {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*\.md$`).MatchString(path.Base(f)) {
			problems = append(problems, f+": reference name must match [a-z][a-z0-9-]*")
		}
		if m := refLinkRE.FindString(text[f]); m != "" {
			problems = append(problems, fmt.Sprintf("%s: links another reference (%s); a reference is one level deep", f, m))
		}
	}
	linked := map[string]bool{}
	for _, core := range cores {
		for _, m := range refLinkRE.FindAllStringSubmatch(text[core], -1) {
			key := m[1] + "/" + m[2]
			if !refs[key] {
				problems = append(problems, fmt.Sprintf("%s: links `jig skill %s %s`, which does not exist", core, m[1], m[2]))
				continue
			}
			if m[1] == path.Dir(core) {
				linked[key] = true
			}
		}
	}
	for key := range refs {
		if !linked[key] {
			problems = append(problems, fmt.Sprintf("%s: reference is not linked from its SKILL.md", key))
		}
	}
	return problems
}

func TestSkillReferenceRules(t *testing.T) {
	core := func(links ...string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("---\nname: x\n---\n" + strings.Join(links, "\n") + "\n")}
	}
	ref := func(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }
	lines := strings.Repeat("a\n", 101)
	bytes := strings.Repeat("a", 6001)
	tests := []struct {
		name string
		tree fstest.MapFS
		want string // substring of one problem; empty means clean
	}{
		{"core only", fstest.MapFS{"x/SKILL.md": core()}, ""},
		{"linked reference", fstest.MapFS{
			"x/SKILL.md":           core("Read `jig skill x deep` when you dig."),
			"x/references/deep.md": ref("text\n"),
		}, ""},
		{"reference naming a whole skill is fine", fstest.MapFS{
			"x/SKILL.md":           core("Read `jig skill x deep` when you dig."),
			"x/references/deep.md": ref("see `jig skill y`\n"),
		}, ""},
		{"unlinked reference", fstest.MapFS{
			"x/SKILL.md":           core(),
			"x/references/deep.md": ref("text\n"),
		}, "not linked"},
		{"linked from another skill only", fstest.MapFS{
			"x/SKILL.md":           core(),
			"y/SKILL.md":           core("Read `jig skill x deep` when you dig."),
			"x/references/deep.md": ref("text\n"),
		}, "not linked"},
		{"link to a missing reference", fstest.MapFS{
			"x/SKILL.md": core("Read `jig skill x gone` when you dig."),
		}, "does not exist"},
		{"reference links onward", fstest.MapFS{
			"x/SKILL.md":           core("Read `jig skill x deep` when a.", "Read `jig skill x more` when b."),
			"x/references/deep.md": ref("then `jig skill x more`\n"),
			"x/references/more.md": ref("text\n"),
		}, "one level deep"},
		{"core over the line cap", fstest.MapFS{"x/SKILL.md": core(lines)}, "lines"},
		{"core over the byte cap", fstest.MapFS{"x/SKILL.md": core(bytes)}, "bytes"},
		{"reference over the line cap", fstest.MapFS{
			"x/SKILL.md":           core("Read `jig skill x deep` when you dig."),
			"x/references/deep.md": ref(lines),
		}, "lines"},
		{"reference over the byte cap", fstest.MapFS{
			"x/SKILL.md":           core("Read `jig skill x deep` when you dig."),
			"x/references/deep.md": ref(bytes),
		}, "bytes"},
		{"bad reference name", fstest.MapFS{
			"x/SKILL.md":             core(),
			"x/references/Deep_1.md": ref("text\n"),
		}, "must match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := skillTreeProblems(tc.tree)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want clean, got %v", got)
				}
				return
			}
			for _, p := range got {
				if strings.Contains(p, tc.want) {
					return
				}
			}
			t.Fatalf("want a problem containing %q, got %v", tc.want, got)
		})
	}
}

func TestSkillReferencesInTheRealTree(t *testing.T) {
	if problems := skillTreeProblems(os.DirFS(repoRoot(t) + "/skills")); len(problems) != 0 {
		t.Fatalf("skills tree: %s", strings.Join(problems, "\n"))
	}
}
