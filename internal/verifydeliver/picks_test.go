package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

// recSpec is one recording a test wants a build to have made: the file it
// wrote and the tags it gave it.
type recSpec struct {
	File, Content, Scenario, Flow, Caption string
	Step                                   int
}

// recordBuild writes specs where a builder's green oracle run at commit leaves
// them, in the run's directory under the jig home, and returns the "recorded"
// journal line that describes them (frontier's finishRecording writes the same
// shape), unappended: a test that wants it in the journal appends it, and one
// that wants it described wrongly edits it first.
func recordBuild(t *testing.T, d Deps, ticket, commit, run string, specs []recSpec) journal.Line {
	t.Helper()
	id, err := d.Store.ID()
	if err != nil {
		t.Fatalf("Store.ID: %v", err)
	}
	dir, err := home.RecordDir(d.Home, id, ticket, commit, run)
	if err != nil {
		t.Fatalf("RecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir the record dir: %v", err)
	}
	line := journal.Line{Slice: "a", Event: "recorded", Commit: commit, Attempt: 1, RecordRun: run}
	for _, s := range specs {
		if err := os.WriteFile(filepath.Join(dir, s.File), []byte(s.Content), 0o644); err != nil {
			t.Fatalf("write %s: %v", s.File, err)
		}
		sum := sha256.Sum256([]byte(s.Content))
		scenario := s.Scenario
		if scenario == "" {
			scenario = strings.TrimSuffix(s.File, filepath.Ext(s.File))
		}
		line.Recordings = append(line.Recordings, journal.Recording{
			File: s.File, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(s.Content)),
			Scenario: scenario, Flow: s.Flow, Step: s.Step, Caption: s.Caption,
		})
	}
	return line
}

// recordedFile is where a recording written by recordBuild lives.
func recordedFile(t *testing.T, d Deps, ticket, commit, run, file string) string {
	t.Helper()
	id, err := d.Store.ID()
	if err != nil {
		t.Fatalf("Store.ID: %v", err)
	}
	dir, err := home.RecordDir(d.Home, id, ticket, commit, run)
	if err != nil {
		t.Fatalf("RecordDir: %v", err)
	}
	return filepath.Join(dir, file)
}

// history makes a small repository: c1 <- c2 on main, and a commit s1 off c1
// that main does not hold.
func history(t *testing.T) (dir, c1, c2, s1 string) {
	t.Helper()
	dir = t.TempDir()
	commit := func(msg string) string {
		t.Helper()
		if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "--allow-empty", "-m", msg); err != nil {
			t.Fatalf("commit %s: %v", msg, err)
		}
		return run(t, dir, "rev-parse", "HEAD")
	}
	run(t, dir, "init", "-b", "main")
	c1 = commit("c1")
	c2 = commit("c2")
	run(t, dir, "checkout", "-b", "side", c1)
	s1 = commit("s1")
	run(t, dir, "checkout", "main")
	return dir, c1, c2, s1
}

// TestPickCandidatesAreTheLatestRecordingOfEachStepOnTheHeadsHistory: of the
// journal's recorded lines only those that wrote files and were not refused
// count, only those whose commit the head holds, and of the files sharing a
// flow, scenario and step the latest line's wins. They come in a stable order
// with ids r1, r2, ... in it.
func TestPickCandidatesAreTheLatestRecordingOfEachStepOnTheHeadsHistory(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	repo, c1, c2, s1 := history(t)

	older := recordBuild(t, d, fx.Ticket, c1, "a-a1-f0", []recSpec{
		{File: "login-old.svg", Content: "login at c1", Scenario: "login", Flow: "onboard", Step: 1},
		{File: "home.svg", Content: "home at c1", Scenario: "home"},
	})
	newer := recordBuild(t, d, fx.Ticket, c2, "b-a1-f0", []recSpec{
		{File: "login.svg", Content: "login at c2", Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in"},
	})
	dropped := recordBuild(t, d, fx.Ticket, c2, "b-a1-f1", []recSpec{
		{File: "profile.mp4", Content: "profile at c2", Scenario: "profile", Flow: "onboard", Step: 2},
	})
	dropped.Outcome = "dropped: notes.txt"
	offBranch := recordBuild(t, d, fx.Ticket, s1, "c-a1-f0", []recSpec{
		{File: "side.svg", Content: "side at s1", Scenario: "side"},
	})
	refused := recordBuild(t, d, fx.Ticket, c2, "b-a1-f2", []recSpec{
		{File: "refused.svg", Content: "refused", Scenario: "refused"},
	})
	refused.Outcome = "refused: a tag is not valid"
	refused.Recordings = nil
	unknown := recordBuild(t, d, fx.Ticket, strings.Repeat("a", 40), "z-a1-f0", []recSpec{
		{File: "unknown.svg", Content: "unknown", Scenario: "unknown"},
	})
	lines := []journal.Line{older, {Event: "oracle", Outcome: "pass"}, newer, dropped, offBranch, refused, unknown}

	cands, droppedNames, err := pickCandidates(d, fx.Ticket, lines, repo, c2)
	if err != nil {
		t.Fatalf("pickCandidates: %v", err)
	}
	if len(droppedNames) != 0 {
		t.Errorf("dropped = %v, want none: every file is as it was recorded", droppedNames)
	}
	type view struct{ ID, Scenario, Flow, Name, Commit, Kind, Caption string }
	var got []view
	for _, c := range cands {
		got = append(got, view{c.ID, c.Scenario, c.Flow, c.Name, c.Commit, c.Kind, c.Caption})
	}
	want := []view{
		{"r1", "home", "", "home.svg", c1, "image", ""},
		{"r2", "login", "onboard", "login.svg", c2, "image", "signs in"},
		{"r3", "profile", "onboard", "profile.mp4", c2, "video", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %+v\nwant         %+v", got, want)
	}
}

// TestPickCandidatesDropsAFileThatNoLongerMatchesItsLine: a candidate is
// checked against its file the way a demo's are at publish, and one that fails
// is no candidate and is named, with why, for publish's output.
func TestPickCandidatesDropsAFileThatNoLongerMatchesItsLine(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	repo, _, c2, _ := history(t)

	line := recordBuild(t, d, fx.Ticket, c2, "a-a1-f0", []recSpec{
		{File: "good.svg", Content: "good"},
		{File: "gone.svg", Content: "gone"},
		{File: "rewritten.svg", Content: "rewritten"},
		{File: "grown.svg", Content: "grown"},
		{File: "folder.svg", Content: "folder"},
		{File: "huge.png", Content: "huge"},
		{File: "notes.txt", Content: "notes"},
	})
	path := func(name string) string { return recordedFile(t, d, fx.Ticket, c2, "a-a1-f0", name) }
	if err := os.Remove(path("gone.svg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path("rewritten.svg"), []byte("REWRITTEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path("grown.svg"), []byte("grown, and then some"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path("folder.svg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path("folder.svg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range line.Recordings {
		switch line.Recordings[i].File {
		case "huge.png":
			line.Recordings[i].Size = media.MaxImageBytes + 1
		}
	}

	cands, dropped, err := pickCandidates(d, fx.Ticket, []journal.Line{line}, repo, c2)
	if err != nil {
		t.Fatalf("pickCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != "good.svg" {
		t.Errorf("candidates = %+v, want only good.svg", cands)
	}
	want := []string{
		"folder.svg (not a regular file)",
		"gone.svg (gone)",
		"grown.svg (changed size)",
		"huge.png (its recorded size is not one that can be attached)",
		"notes.txt (not an accepted type)",
		"rewritten.svg (changed)",
	}
	if !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped = %q\nwant      %q", dropped, want)
	}
}

// TestPickCandidatesRefuseAJournaledNameThatClimbsOutOfTheRecordingDirectory:
// the journal is the store's, shared and editable, so a file name or a run it
// names is never trusted to stay inside the recording directories.
func TestPickCandidatesRefuseAJournaledNameThatClimbsOutOfTheRecordingDirectory(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	repo, _, c2, _ := history(t)

	line := recordBuild(t, d, fx.Ticket, c2, "a-a1-f0", []recSpec{{File: "ok.svg", Content: "ok"}})
	line.Recordings = append(line.Recordings, journal.Recording{File: "../ok.svg", SHA256: line.Recordings[0].SHA256, Size: 2, Scenario: "up"})
	badRun := line
	badRun.RecordRun = "../a-a1-f0"

	cands, dropped, err := pickCandidates(d, fx.Ticket, []journal.Line{line}, repo, c2)
	if err != nil {
		t.Fatalf("pickCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != "ok.svg" || len(dropped) != 1 || !strings.HasPrefix(dropped[0], "../ok.svg") {
		t.Errorf("candidates = %+v, dropped = %q; want ok.svg alone and the climbing name dropped", cands, dropped)
	}
	cands, dropped, err = pickCandidates(d, fx.Ticket, []journal.Line{badRun}, repo, c2)
	if err != nil {
		t.Fatalf("pickCandidates: %v", err)
	}
	if len(cands) != 0 || len(dropped) != 2 {
		t.Errorf("a run that climbs gave candidates %+v, dropped %q; want none, both dropped", cands, dropped)
	}
}

// pickTestCands is a candidate set for the validation and render tests: two
// steps of a flow, a lone scenario, and a video.
func pickTestCands() []pickCandidate {
	mk := func(id, scenario, flow string, step int, file, caption string) pickCandidate {
		return pickCandidate{
			PicksCandidate: PicksCandidate{ID: id, Scenario: scenario, Flow: flow, Step: step, Caption: caption, Commit: strings.Repeat("c", 40), Name: file},
			run:            "a-a1-f0",
			rec:            journal.Recording{File: file, SHA256: strings.Repeat("0", 64), Size: 4, Scenario: scenario, Flow: flow, Step: step, Caption: caption},
		}
	}
	return []pickCandidate{
		mk("r1", "home", "", 0, "home.svg", ""),
		mk("r2", "login", "onboard", 1, "login.svg", "signs in"),
		mk("r3", "profile", "onboard", 2, "profile.mp4", ""),
		mk("r4", "settings", "onboard", 3, "settings.png", "saves"),
	}
}

// TestParsePicksResultIsStrict: a result is one JSON object with a flows list
// and a summary, no key repeated and none but the recognized ones at any level.
func TestParsePicksResultIsStrict(t *testing.T) {
	t.Parallel()
	good := `{"flows":[{"title":"T","items":[{"id":"r1"},{"id":"r2","caption":"c"}]}],"summary":"S"}`
	res, err := ParsePicksResult([]byte(good))
	if err != nil {
		t.Fatalf("a good result: %v", err)
	}
	want := PicksResult{Flows: []PicksFlow{{Title: "T", Items: []PicksItem{{ID: "r1"}, {ID: "r2", Caption: "c"}}}}, Summary: "S"}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	bad := map[string]string{
		"empty":             ``,
		"an array":          `[]`,
		"not JSON":          `{"flows":`,
		"two objects":       good + good,
		"a repeated key":    `{"flows":[],"flows":[],"summary":"S"}`,
		"a case-only twin":  `{"flows":[],"Flows":[],"summary":"S"}`,
		"flows missing":     `{"summary":"S"}`,
		"summary missing":   `{"flows":[]}`,
		"flows null":        `{"flows":null,"summary":"S"}`,
		"summary null":      `{"flows":[],"summary":null}`,
		"flows not a list":  `{"flows":"x","summary":"S"}`,
		"summary not text":  `{"flows":[],"summary":7}`,
		"unknown top key":   `{"flows":[],"summary":"S","extra":1}`,
		"unknown flow key":  `{"flows":[{"title":"T","items":[],"extra":1}],"summary":"S"}`,
		"unknown item key":  `{"flows":[{"title":"T","items":[{"id":"r1","extra":1}]}],"summary":"S"}`,
		"a case variant":    `{"flows":[{"Title":"T","items":[]}],"summary":"S"}`,
		"an item not a map": `{"flows":[{"title":"T","items":["r1"]}],"summary":"S"}`,
	}
	for name, data := range bad {
		if _, err := ParsePicksResult([]byte(data)); err == nil {
			t.Errorf("%s: parsed, want a refusal", name)
		} else if !strings.HasPrefix(err.Error(), "the pick result is invalid: ") {
			t.Errorf("%s: error = %q, want jig's own wording", name, err)
		}
	}
}

// TestResolvePicksChecksTheResultAgainstTheCandidates: every check is jig's
// and every refusal is in jig's words, naming a flow and an item by position.
func TestResolvePicksChecksTheResultAgainstTheCandidates(t *testing.T) {
	t.Parallel()
	cands := pickTestCands()
	item := func(id string) PicksItem { return PicksItem{ID: id} }
	flow := func(title string, items ...PicksItem) PicksFlow { return PicksFlow{Title: title, Items: items} }
	many := make([]pickCandidate, media.MaxFiles+1)
	manyItems := make([]PicksItem, len(many))
	for i := range many {
		many[i] = pickCandidate{PicksCandidate: PicksCandidate{ID: fmt.Sprintf("r%d", i+1), Scenario: fmt.Sprintf("s%d", i)}}
		manyItems[i] = item(many[i].ID)
	}

	cases := []struct {
		name  string
		cands []pickCandidate
		res   PicksResult
		want  string // "" for accepted, else a fragment of the refusal
	}{
		{"a flow in step order", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("r2"), item("r3"), item("r4"))}}, ""},
		{"a step repeated", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("r2"), item("r2"))}}, "already picked"},
		{"lone and connected mixed", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("A", item("r3")), flow("B", item("r1"), item("r2"))}}, ""},
		{"no flows", cands, PicksResult{Summary: "S"}, "it picked no recording"},
		{"a flow with no items", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T")}}, "flow 1 has no recording"},
		{"an unknown id", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("r2"), item("r9"))}}, "flow 1 item 2 is not a candidate"},
		{"an id used in two flows", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("A", item("r1")), flow("B", item("r1"))}}, "flow 2 item 1 is a candidate that is already picked"},
		{"steps out of order", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("r3"), item("r2"))}}, "flow 1 item 2 comes after a later step"},
		{"steps of different flows in any order", append(append([]pickCandidate{}, cands...), pickCandidate{PicksCandidate: PicksCandidate{ID: "r5", Scenario: "x", Flow: "other", Step: 1}}),
			PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("r3"), item("r5"))}}, ""},
		{"an empty title", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow(" \t", item("r1"))}}, "flow 1 has an empty title"},
		{"a title over its bound", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow(strings.Repeat("t", maxPickTitleBytes+1), item("r1"))}}, "title is"},
		{"an empty summary", cands, PicksResult{Summary: " ", Flows: []PicksFlow{flow("T", item("r1"))}}, "the summary is empty"},
		{"a summary over its bound", cands, PicksResult{Summary: strings.Repeat("s", maxPickSummaryBytes+1), Flows: []PicksFlow{flow("T", item("r1"))}}, "the summary is"},
		{"a blank caption", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", PicksItem{ID: "r1", Caption: "  "})}}, "flow 1 item 1 has a blank caption"},
		{"a caption over its bound", cands, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", PicksItem{ID: "r1", Caption: strings.Repeat("c", maxPickCaptionBytes+1)})}}, "caption is"},
		{"as many files as are accepted", many[:media.MaxFiles], PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", manyItems[:media.MaxFiles]...)}}, ""},
		{"one file too many", many, PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", manyItems...)}}, "at most 50"},
	}
	for _, c := range cases {
		p, err := resolvePicks(c.res, c.cands)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.want == "" && len(p.items()) == 0:
			t.Errorf("%s: accepted with no items", c.name)
		case c.want != "" && err == nil:
			t.Errorf("%s: accepted, want a refusal naming %q", c.name, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: error = %q, want it to name %q", c.name, err, c.want)
		}
	}

	// A refusal never repeats the session's own text.
	_, err := resolvePicks(PicksResult{Summary: "S", Flows: []PicksFlow{flow("T", item("/home/me/secret"))}}, cands)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("an unknown id's refusal = %v, want one that does not repeat the id", err)
	}
}

// TestAJournaledPickIsTheResultItWasMadeFrom: reusing a pick passes the
// journal's copy through the same resolution a fresh result gets, and what it
// resolves to is what was picked.
func TestAJournaledPickIsTheResultItWasMadeFrom(t *testing.T) {
	t.Parallel()
	cands := pickTestCands()
	res := PicksResult{Summary: "S", Flows: []PicksFlow{
		{Title: "Onboarding", Items: []PicksItem{{ID: "r2"}, {ID: "r3", Caption: "profile"}}},
		{Title: "Home", Items: []PicksItem{{ID: "r1"}}},
	}}
	p, err := resolvePicks(res, cands)
	if err != nil {
		t.Fatal(err)
	}
	fp := pickFingerprint(cands)
	jp := journalPick(p, fp)
	want := &journal.Pick{Candidates: fp, Summary: "S", Flows: []journal.PickFlow{
		{Title: "Onboarding", Items: []journal.PickItem{{ID: "r2", File: "login.svg"}, {ID: "r3", File: "profile.mp4", Caption: "profile"}}},
		{Title: "Home", Items: []journal.PickItem{{ID: "r1", File: "home.svg"}}},
	}}
	if !reflect.DeepEqual(jp, want) {
		t.Errorf("journalPick = %+v, want %+v", jp, want)
	}
	again, err := resolvePicks(picksResultOf(jp), cands)
	if err != nil || !reflect.DeepEqual(journalPick(again, fp), jp) {
		t.Errorf("the journaled pick resolved to %+v, %v; want the pick it came from", again, err)
	}

	// The fingerprint follows everything a pick of the candidates depends on.
	changed := pickTestCands()
	changed[1].rec.SHA256 = strings.Repeat("1", 64)
	if pickFingerprint(changed) == fp {
		t.Error("the fingerprint ignores a recording's content")
	}
	moved := pickTestCands()
	moved[2].Step = 9
	if pickFingerprint(moved) == fp {
		t.Error("the fingerprint ignores a recording's step")
	}
	if pickFingerprint(pickTestCands()) != fp {
		t.Error("the same candidates fingerprint differently")
	}
}

// TestReusablePickIsTheLatestForTheSameHeadAndRecordings.
func TestReusablePickIsTheLatestForTheSameHeadAndRecordings(t *testing.T) {
	t.Parallel()
	old := &journal.Pick{Candidates: "fp1", Summary: "old"}
	newer := &journal.Pick{Candidates: "fp1", Summary: "newer"}
	other := &journal.Pick{Candidates: "fp2", Summary: "other"}
	lines := []journal.Line{
		{Event: "publish-picks", Commit: "h1", Outcome: PicksPicked, Pick: old},
		{Event: "publish-picks", Commit: "h1", Outcome: PicksReused, Pick: newer},
		{Event: "publish-picks", Commit: "h2", Outcome: PicksPicked, Pick: other},
		{Event: "publish-picks", Commit: "h1", Outcome: "refused: it picked nothing"},
	}
	if got := reusablePick(lines, "h1", "fp1"); got != newer {
		t.Errorf("reusablePick(h1, fp1) = %+v, want the latest pick for it, a refusal after it notwithstanding", got)
	}
	if got := reusablePick(lines, "h1", "fp2"); got != nil {
		t.Errorf("reusablePick for other candidates = %+v, want none", got)
	}
	if got := reusablePick(lines, "h3", "fp1"); got != nil {
		t.Errorf("reusablePick for another head = %+v, want none", got)
	}
}

// TestRenderPicksPromptNamesTheFilesAndTheContract.
func TestRenderPicksPromptNamesTheFilesAndTheContract(t *testing.T) {
	t.Parallel()
	got := RenderPicksPrompt("/h/picks.json", "/h/result.json")
	for _, want := range []string{
		"picks.json at /h/picks.json",
		"write /h/result.json with exactly one JSON object: " + picksResultSchema,
		intentSourcesPrompt,
		fmt.Sprintf("at most %d recordings", media.MaxFiles),
		"step order",
		"refused whole",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\u2014") {
		t.Error("prompt holds an em dash")
	}
}

// TestRenderPicksSectionComposesFlowsInOrderAndMakesTheWordsSafe: flows come
// under their own ### headings, each recording in the one reference form gh
// rewrites for its kind, and the session's words get the treatment a demo's
// get: one line, no link characters, capped, HTML-escaped, and host paths left
// out whole and named.
func TestRenderPicksSectionComposesFlowsInOrderAndMakesTheWordsSafe(t *testing.T) {
	t.Parallel()
	cands := pickTestCands()
	secret := filepath.Join(t.TempDir(), "jig-home")
	res := PicksResult{
		Summary: "Shows <b>the</b> change.\n\n# A heading",
		Flows: []PicksFlow{
			{Title: "Onboarding [flow]", Items: []PicksItem{{ID: "r2"}, {ID: "r3", Caption: "profile\nsaved & shown"}, {ID: "r4", Caption: "see " + secret}}},
			{Title: "Where " + secret + " lives", Items: []PicksItem{{ID: "r1"}}},
		},
	}
	p, err := resolvePicks(res, cands)
	if err != nil {
		t.Fatal(err)
	}
	files := []DemoFile{{Name: "rec-1.svg"}, {Name: "rec-2.mp4"}, {Name: "rec-3.png"}, {Name: "rec-4.svg"}}
	out := renderPicksSection(p, "/stage", files, []hostDir{{secret, "<jig home>"}})

	want := "## Demo\n\n" +
		"Shows &lt;b&gt;the&lt;/b&gt; change.\n\n### A heading\n\n" +
		"### Onboarding flow\n\n" +
		"- ![signs in](./rec-1.svg)\n" +
		"- ./rec-2.mp4: profile saved &amp; shown\n" +
		"- ![](./rec-3.png)\n\n" +
		"### Recordings\n\n" +
		"- ![home](./rec-4.svg)\n\n"
	if out.Section != want {
		t.Errorf("section:\n%s\nwant:\n%s", out.Section, want)
	}
	if out.MediaDir != "/stage" || !reflect.DeepEqual(out.MediaFiles, files) {
		t.Errorf("MediaDir = %q, MediaFiles = %+v; want the staging directory and the files", out.MediaDir, out.MediaFiles)
	}
	if out.ScrubbedSummary {
		t.Error("the summary names no directory yet was reported scrubbed")
	}
	if wantScrubbed := []string{"rec-3.png", "the title of flow 2"}; !reflect.DeepEqual(out.ScrubbedCaptions, wantScrubbed) {
		t.Errorf("ScrubbedCaptions = %q, want %q", out.ScrubbedCaptions, wantScrubbed)
	}
	if strings.Contains(out.Section, secret) {
		t.Error("the section names a host directory")
	}

	// A summary that names one is left out whole.
	p.summary = "recorded under " + secret
	if out := renderPicksSection(p, "/stage", files, []hostDir{{secret, "<jig home>"}}); !out.ScrubbedSummary || strings.Contains(out.Section, "recorded under") || !strings.HasPrefix(out.Section, "## Demo\n\n### Onboarding") {
		t.Errorf("a summary naming a host directory was kept: scrubbed=%v\n%s", out.ScrubbedSummary, out.Section)
	}
}
