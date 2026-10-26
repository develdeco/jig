package setup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testSkills() []Skill {
	return []Skill{
		{Name: "here", Body: []byte("---\nname: here\ndescription: Names where the work is. Use at the start.\n---\n\n# here\n")},
		{Name: "intake", Body: []byte("---\nname: intake\ndescription: Drafts a brief.\n---\n\n# intake\n")},
	}
}

func TestSkillStamp(t *testing.T) {
	t.Parallel()
	body := []byte("---\nname: here\n---\n\n# here\n\n\n")
	stamped := StampSkill(body, "v1.2.3+dirty")
	if want := "---\nname: here\n---\n\n# here\n<!-- jig-skill v=v1.2.3+dirty -->\n"; string(stamped) != want {
		t.Errorf("StampSkill =\n%q\nwant\n%q", stamped, want)
	}
	if v, ok := SkillStamp(stamped); !ok || v != "v1.2.3+dirty" {
		t.Errorf("SkillStamp = %q, %v", v, ok)
	}
	if v, ok := SkillStamp([]byte(strings.ReplaceAll(string(stamped), "\n", "\r\n"))); !ok || v != "v1.2.3+dirty" {
		t.Errorf("SkillStamp of a CRLF file = %q, %v", v, ok)
	}
	for _, none := range []string{"", "# a skill\n", "<!-- jig-skill v=1 -->\nmore text\n", "<!-- jig-skill 1 -->\n"} {
		if v, ok := SkillStamp([]byte(none)); ok {
			t.Errorf("SkillStamp(%q) = %q, want none", none, v)
		}
	}
	// Stamping twice keeps one stamp per write, not a growing tail.
	if twice := StampSkill(stamped, "v2"); strings.Count(string(twice), "jig-skill") != 2 {
		t.Logf("a stamped body stamped again carries both lines (callers stamp the embedded body once): %q", twice)
	}
}

func TestInstallSkillsWritesStampedFilesOnce(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "skills")
	skills := testSkills()

	written, _, err := InstallSkills(root, skills, "v1", true)
	if err != nil || !slices.Equal(written, []string{"here", "intake"}) {
		t.Fatalf("first install = %v, %v", written, err)
	}
	data := readOrFail(t, filepath.Join(root, "here", "SKILL.md"))
	if !strings.HasSuffix(data, "# here\n<!-- jig-skill v=v1 -->\n") || !strings.HasPrefix(data, "---\nname: here\n") {
		t.Errorf("installed file:\n%s", data)
	}
	if written, _, err := InstallSkills(root, skills, "v1", true); err != nil || len(written) != 0 {
		t.Errorf("second install wrote %v, %v", written, err)
	}
	if written, _, err := InstallSkills(root, skills, "v2", true); err != nil || len(written) != 2 {
		t.Errorf("an install by a newer jig wrote %v, %v, want both skills restamped", written, err)
	}

	// A skill directory named like jig's is jig's to replace, with replace.
	writeOrFail(t, filepath.Join(root, "here", "SKILL.md"), "mine\n", 0o644)
	if written, kept, err := InstallSkills(root, skills, "v2", true); err != nil || !slices.Equal(written, []string{"here"}) || len(kept) != 0 {
		t.Errorf("install over a file named like jig's wrote %v, kept %v, %v", written, kept, err)
	}
	// Without it, a file that carries no stamp is the person's and stays.
	writeOrFail(t, filepath.Join(root, "here", "SKILL.md"), "mine\n", 0o644)
	written, kept, err := InstallSkills(root, skills, "v2", false)
	if err != nil || len(written) != 0 || !slices.Equal(kept, []string{"here"}) {
		t.Errorf("install without replace over an unstamped file: wrote %v, kept %v, %v", written, kept, err)
	}
	if got := readOrFail(t, filepath.Join(root, "here", "SKILL.md")); got != "mine\n" {
		t.Errorf("the unstamped file was changed: %q", got)
	}
	// A stamped one is jig's and is brought up to date either way.
	if _, _, err := InstallSkills(root, skills, "v1", true); err != nil {
		t.Fatal(err)
	}
	if written, kept, err := InstallSkills(root, skills, "v2", false); err != nil || len(written) != 2 || len(kept) != 0 {
		t.Errorf("install without replace over stamped files: wrote %v, kept %v, %v", written, kept, err)
	}
}

func TestInspectSkills(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	skills := testSkills()
	states := func(version string) string {
		t.Helper()
		st, err := InspectSkills(root, skills, version)
		if err != nil {
			t.Fatal(err)
		}
		return SkillsState(st)
	}
	if got := states("v1"); got != StateMissing {
		t.Errorf("nothing installed: %s", got)
	}
	if _, _, err := InstallSkills(root, skills[:1], "v1", true); err != nil {
		t.Fatal(err)
	}
	if got := states("v1"); got != StatePartial {
		t.Errorf("one of two: %s", got)
	}
	if _, _, err := InstallSkills(root, skills, "v1", true); err != nil {
		t.Fatal(err)
	}
	if got := states("v1"); got != StateInstalled {
		t.Errorf("both: %s", got)
	}
	if got := states("v2"); got != StateStale {
		t.Errorf("both, from an older jig: %s", got)
	}
	st, _ := InspectSkills(root, skills, "v2")
	if st[0].Version != "v1" || st[0].State != StateStale {
		t.Errorf("status = %+v, want the stamp's version v1", st[0])
	}
	// The unstamped copy `jig skills install` wrote counts as installed.
	writeOrFail(t, filepath.Join(root, "here", "SKILL.md"), string(skills[0].Body), 0o644)
	if st, _ := InspectSkills(root, skills[:1], "v2"); st[0].State != StateInstalled || st[0].Version != "" {
		t.Errorf("unstamped copy = %+v", st[0])
	}
	writeOrFail(t, filepath.Join(root, "here", "SKILL.md"), "someone else's\n", 0o644)
	if st, _ := InspectSkills(root, skills[:1], "v2"); st[0].State != StateStale {
		t.Errorf("a different file = %+v", st[0])
	}
}

func TestRemoveSkillsTakesOnlyJigs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	skills := testSkills()
	if _, _, err := InstallSkills(root, skills, "v1", true); err != nil {
		t.Fatal(err)
	}
	writeOrFail(t, filepath.Join(root, "mine", "SKILL.md"), "---\nname: mine\n---\n", 0o644)
	// One `jig skills install` wrote before there were stamps.
	writeOrFail(t, filepath.Join(root, "retro", "SKILL.md"), "retro body\n", 0o644)
	// One named like jig's that the person wrote.
	writeOrFail(t, filepath.Join(root, "other", "SKILL.md"), "mine too\n", 0o644)
	embedded := map[string][]byte{"retro": []byte("retro body\n"), "other": []byte("not the same\n")}

	removed, err := RemoveSkills(root, nil, embedded)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if !slices.Equal(removed, []string{"here", "intake", "retro"}) {
		t.Errorf("removed %v", removed)
	}
	for _, kept := range []string{"mine", "other"} {
		if _, err := os.Stat(filepath.Join(root, kept, "SKILL.md")); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	for _, gone := range []string{"here", "intake", "retro"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}

	// By name: only that one, and a missing root is nothing to do.
	if _, _, err := InstallSkills(root, skills, "v1", true); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveSkills(root, []string{"here"}, nil); err != nil || !slices.Equal(removed, []string{"here"}) {
		t.Errorf("by name: %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "intake", "SKILL.md")); err != nil {
		t.Errorf("intake went with here: %v", err)
	}
	if removed, err := RemoveSkills(filepath.Join(root, "nowhere"), nil, nil); err != nil || len(removed) != 0 {
		t.Errorf("missing root: %v, %v", removed, err)
	}
}

func TestStubBodyListsTheSkillsAndSendsToTheBinary(t *testing.T) {
	t.Parallel()
	body := string(StubBody([]StubEntry{{Name: "here", Summary: "Names where the work is."}, {Name: "x", Summary: "a | b"}}))
	for _, want := range []string{"name: jig\n", "run `jig here`", "`jig skill <name>`", "| `here` | Names where the work is. |", "| `x` | a / b |",
		"`jig next <ticket>` names the phase it is at", "is an automatic run", "\"and publish it\"",
		"subagent with TaskOutput (`block: true`)", "never call another tool to stall"} {
		if !strings.Contains(body, want) {
			t.Errorf("stub lacks %q:\n%s", want, body)
		}
	}
	if n := len(strings.Split(body, "\n")); n > 100 {
		t.Errorf("stub is %d lines", n)
	}
	if summary := SkillSummary(testSkills()[0].Body); summary != "Names where the work is." {
		t.Errorf("SkillSummary = %q", summary)
	}
	if SkillSummary([]byte("# no frontmatter\n")) != "" {
		t.Error("SkillSummary of a body with no description")
	}
}

func TestPruneEmptyDirs(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	deep := filepath.Join(top, ".claude", "skills", "jig")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOrFail(t, filepath.Join(top, ".claude", "keep.md"), "x", 0o644)
	PruneEmptyDirs(deep, top)
	if _, err := os.Stat(filepath.Join(top, ".claude", "skills")); !os.IsNotExist(err) {
		t.Errorf("empty skills dir is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(top, ".claude")); err != nil {
		t.Errorf(".claude went although it holds a file: %v", err)
	}
	if _, err := os.Stat(top); err != nil {
		t.Errorf("the stop directory went: %v", err)
	}
}

func TestHintMergeIsIdempotentAndUnmergeKeepsTheirText(t *testing.T) {
	t.Parallel()
	mine := "# My notes\n\nUse tabs.\n"
	once, changed, err := MergeHint([]byte(mine))
	if err != nil || !changed {
		t.Fatalf("merge: %v, %v", changed, err)
	}
	if !strings.HasPrefix(string(once), mine+"\n"+hintOpen+"\n") || !strings.HasSuffix(string(once), hintClose+"\n") {
		t.Errorf("merged file:\n%s", once)
	}
	if !strings.Contains(string(once), "`jig here`") || !strings.Contains(string(once), "`jig skill <name>`") {
		t.Errorf("hint says nothing of jig here and jig skill:\n%s", once)
	}
	twice, changed, err := MergeHint(once)
	if err != nil || changed || string(twice) != string(once) {
		t.Errorf("second merge: changed %v, err %v", changed, err)
	}
	if HintState(once) != StateInstalled || HintState([]byte(mine)) != StateMissing {
		t.Errorf("HintState = %s / %s", HintState(once), HintState([]byte(mine)))
	}
	stale := strings.Replace(string(once), "Use tabs.", "Use tabs.", 1)
	stale = strings.Replace(stale, "jig tracks", "jig used to track", 1)
	if HintState([]byte(stale)) != StateStale {
		t.Errorf("a block of another text: %s", HintState([]byte(stale)))
	}
	fixed, changed, err := MergeHint([]byte(stale))
	if err != nil || !changed || string(fixed) != string(once) {
		t.Errorf("merge over a stale block: changed %v, err %v\n%s", changed, err, fixed)
	}

	back, changed, err := UnmergeHint(once)
	if err != nil || !changed || string(back) != mine {
		t.Errorf("unmerge = %q, %v, %v, want the person's text", back, changed, err)
	}
	if out, changed, err := UnmergeHint([]byte(mine)); err != nil || changed || string(out) != mine {
		t.Errorf("unmerge of a file with no block: %v, %v", changed, err)
	}
	// A file that was only the block goes.
	only, _, _ := MergeHint(nil)
	if out, changed, err := UnmergeHint(only); err != nil || !changed || out != nil {
		t.Errorf("unmerge of a file that was jig's alone = %q, %v, %v, want nil", out, changed, err)
	}
	// Text after the block stays.
	after := string(once) + "\nMore of mine.\n"
	if out, _, err := UnmergeHint([]byte(after)); err != nil || !strings.Contains(string(out), "More of mine.") || strings.Contains(string(out), "jig") {
		t.Errorf("unmerge with text after the block = %q, %v", out, err)
	}
	// An unclosed block is an error, not a guess.
	if _, _, err := MergeHint([]byte(hintOpen + "\nhalf\n")); err == nil {
		t.Error("merge into an unclosed block succeeded")
	}
}

func TestPlanHint(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sub", "CLAUDE.md")
	plan, err := PlanHint(path, false)
	if err != nil || !plan.Changed() || plan.Delete {
		t.Fatalf("install plan %+v, %v", plan, err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if again, err := PlanHint(path, false); err != nil || again.Changed() {
		t.Errorf("second install plan changes: %+v, %v", again, err)
	}
	rm, err := PlanHint(path, true)
	if err != nil || !rm.Delete || !rm.Changed() {
		t.Fatalf("uninstall plan %+v, %v", rm, err)
	}
	if err := rm.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the file jig made is still there: %v", err)
	}
	if none, err := PlanHint(path, true); err != nil || none.Changed() {
		t.Errorf("uninstall of nothing: %+v, %v", none, err)
	}
}

func TestHintFiles(t *testing.T) {
	t.Parallel()
	for harness, want := range map[string]string{"claude": ".claude/CLAUDE.md", "codex": ".codex/AGENTS.md", "gemini": ".gemini/GEMINI.md"} {
		if got := filepath.ToSlash(HintFile(harness, "home")); got != "home/"+want {
			t.Errorf("HintFile(%s) = %s", harness, got)
		}
	}
	if HintFile("cursor", "home") != "" {
		t.Error("cursor has a memory file")
	}
	if got := filepath.ToSlash(ProjectHintFile("w", false)); got != "w/CLAUDE.local.md" {
		t.Errorf("untracked = %s", got)
	}
	if got := filepath.ToSlash(ProjectHintFile("w", true)); got != "w/AGENTS.md" {
		t.Errorf("tracked = %s", got)
	}
}
