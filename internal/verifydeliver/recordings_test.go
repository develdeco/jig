package verifydeliver

import (
	"encoding/json"
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
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// recordingsLease is a lease whose history a test records into: seed <- c1 <-
// c2 on main, with c2 the head the reviewer reviews, and a commit s1 off the
// seed that main does not hold.
type recordingsLease struct {
	dir          string
	seed, c1, c2 string
	s1           string
}

func newRecordingsLease(t *testing.T) recordingsLease {
	t.Helper()
	l := recordingsLease{dir: newReviewLease(t, "main")}
	var err error
	if l.seed, err = gitx.RevParse(l.dir, "HEAD"); err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, l.dir, "a.go", "a")
	l.c1 = commitReviewLease(t, l.dir, "c1")
	writeReviewFile(t, l.dir, "b.go", "b")
	l.c2 = commitReviewLease(t, l.dir, "c2")
	if _, err := gitx.RunEnv(l.dir, reviewGitEnv, "checkout", "-b", "side", l.seed); err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, l.dir, "side.go", "side")
	l.s1 = commitReviewLease(t, l.dir, "s1")
	if _, err := gitx.RunEnv(l.dir, reviewGitEnv, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	return l
}

// journalLines appends lines to ticket's journal in the store.
func journalLines(t *testing.T, st *store.Store, ticket string, lines ...journal.Line) {
	t.Helper()
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if err := journal.Append(st, ticket, l); err != nil {
			t.Fatalf("journal.Append: %v", err)
		}
	}
}

// dispatchedRound runs one reviewer round over in with a backend that answers
// the minimal valid result, and returns the dispatch it was handed and the
// bytes of the review.json it was handed.
func dispatchedRound(t *testing.T, in RoundInput) (session.Dispatch, []byte) {
	t.Helper()
	var (
		got        session.Dispatch
		reviewJSON []byte
	)
	backend := stubBackend{run: func(d session.Dispatch) error {
		got = d
		var err error
		if reviewJSON, err = os.ReadFile(d.SliceJSON); err != nil {
			t.Errorf("read review.json: %v", err)
		}
		writeMustReviewResult(t, d)
		return nil
	}}
	if _, ok, err := NewReviewerGateSource(backend).Round(in); err != nil || !ok {
		t.Fatalf("Round: ok = %v, err = %v", ok, err)
	}
	if got.SliceJSON == "" {
		t.Fatal("the reviewer was never dispatched")
	}
	return got, reviewJSON
}

// roundInput is the reviewer round's input over l, with the jig home jigHome
// ("" for none).
func (l recordingsLease) roundInput(st *store.Store, jigHome string) RoundInput {
	return RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: l.dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(), Home: jigHome,
	}
}

// reviewListPath is where the round's recordings.json belongs.
func reviewListPath(t *testing.T, st *store.Store, jigHome string, round int) string {
	t.Helper()
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := home.ReviewDir(jigHome, id, "JIG-1", round)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "recordings.json")
}

// recordedList reads the recordings.json a round wrote at path.
func recordedList(t *testing.T, path string) ReviewRecordings {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recordings.json: %v", err)
	}
	var list ReviewRecordings
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("parse recordings.json: %v\n%s", err, data)
	}
	return list
}

// TestReviewerRoundListsTheRecordingsOfTheHeadsHistoryAndNamesThemInThePrompt:
// a round whose head's history holds recordings writes a recordings.json under
// the jig home (not in the store) listing the latest recording of each flow,
// scenario and step made at the head or an ancestor, latest first, each with the
// absolute path of its file, and the prompt names that file in one added
// paragraph and is otherwise the prompt a round without recordings gets.
func TestReviewerRoundListsTheRecordingsOfTheHeadsHistoryAndNamesThemInThePrompt(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}

	older := recordBuild(t, d, "JIG-1", l.c1, "a-a1-f0", []recSpec{
		{File: "login-old.svg", Content: "login at c1", Scenario: "login", Flow: "onboard", Step: 1},
		{File: "home.svg", Content: "home at c1", Scenario: "home"},
		{File: "rewritten.svg", Content: "as recorded", Scenario: "rewritten"},
	})
	newer := recordBuild(t, d, "JIG-1", l.c2, "b-a1-f0", []recSpec{
		{File: "login.svg", Content: "login at c2", Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in"},
		{File: "profile.mp4", Content: "profile at c2", Scenario: "profile", Flow: "onboard", Step: 2},
	})
	offBranch := recordBuild(t, d, "JIG-1", l.s1, "c-a1-f0", []recSpec{{File: "side.svg", Content: "side at s1"}})
	journalLines(t, st, "JIG-1", older, journal.Line{Event: "oracle", Outcome: "pass"}, newer, offBranch)
	// The file a line describes changed after it was recorded: it is not evidence of that run.
	if err := os.WriteFile(recordedFile(t, d, "JIG-1", l.c1, "a-a1-f0", "rewritten.svg"), []byte("CHANGED"), 0o644); err != nil {
		t.Fatal(err)
	}

	disp, reviewJSON := dispatchedRound(t, l.roundInput(st, jigHome))

	path := reviewListPath(t, st, jigHome, 1)
	list := recordedList(t, path)
	want := []ReviewRecording{
		{Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in", Commit: l.c2, Kind: "image", File: recordedFile(t, d, "JIG-1", l.c2, "b-a1-f0", "login.svg")},
		{Scenario: "profile", Flow: "onboard", Step: 2, Commit: l.c2, Kind: "video", File: recordedFile(t, d, "JIG-1", l.c2, "b-a1-f0", "profile.mp4")},
		{Scenario: "home", Commit: l.c1, Kind: "image", File: recordedFile(t, d, "JIG-1", l.c1, "a-a1-f0", "home.svg")},
	}
	if !reflect.DeepEqual(list.Recordings, want) {
		t.Errorf("recordings =\n%+v\nwant (the latest of each step on the head's history, the latest build's first, a build's own in flow and step order)\n%+v", list.Recordings, want)
	}
	if list.Ticket != "JIG-1" || list.HeadSHA != l.c2 || list.Omitted != 0 {
		t.Errorf("recordings.json = %+v, want ticket JIG-1, head %s, nothing omitted", list, l.c2)
	}
	for _, r := range list.Recordings {
		if !filepath.IsAbs(r.File) {
			t.Errorf("file %q is not an absolute path", r.File)
		}
		if _, err := os.Stat(r.File); err != nil {
			t.Errorf("a listed file is not there: %v", err)
		}
	}

	// The list is the jig home's, never the store's: it names files of this machine.
	if strings.HasPrefix(path, st.Root) {
		t.Errorf("recordings.json at %q is inside the store %q", path, st.Root)
	}
	if strings.Contains(string(reviewJSON), "recordings") {
		t.Errorf("review.json mentions the recordings; the prompt names them:\n%s", reviewJSON)
	}
	err := filepath.WalkDir(st.Root, func(p string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.Contains(de.Name(), "recordings") {
			t.Errorf("the store holds %q", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// The prompt differs from a round without recordings by one paragraph that names the file.
	req := readReviewRequest(t, disp.SliceJSON)
	paragraph := "\n" + fmt.Sprintf(reviewRecordingsParagraph, path)
	if !strings.Contains(disp.Prompt, paragraph) {
		t.Fatalf("the prompt does not hold the recordings paragraph for %s:\n%s", path, disp.Prompt)
	}
	if got, want := strings.Replace(disp.Prompt, paragraph, "", 1), RenderReviewPrompt(req, disp.SliceJSON, disp.ResultJSON); got != want {
		t.Errorf("the prompt without its paragraph is not the prompt of a round without recordings:\n%s\nwant\n%s", got, want)
	}
	for _, phrase := range []string{"recordings.json at " + path, "<text> elements", "a finding still needs a cause in the diff"} {
		if !strings.Contains(disp.Prompt, phrase) {
			t.Errorf("the prompt does not say %q", phrase)
		}
	}
	// The dispatch carries the list for the backends that spell the prompt's paths for their session.
	if disp.ExtraReadFile != path {
		t.Errorf("ExtraReadFile = %q, want the list %q", disp.ExtraReadFile, path)
	}
}

// TestReviewerRoundWithoutRecordingsHandsTheReviewerWhatItAlwaysHas: a round
// whose head's history holds no recording - there is no jig home, none were
// made, or only a commit off the head's history has any - gets the same
// review.json and the same prompt, to the byte, as a round that never heard of
// recordings, and writes nothing under the jig home.
func TestReviewerRoundWithoutRecordingsHandsTheReviewerWhatItAlwaysHas(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}

	baseDisp, baseJSON := dispatchedRound(t, l.roundInput(st, ""))
	if baseDisp.ExtraReadFile != "" {
		t.Errorf("a round with no jig home names a file to read: %q", baseDisp.ExtraReadFile)
	}
	if strings.Contains(baseDisp.Prompt, "recordings") {
		t.Fatalf("a round with no jig home mentions recordings:\n%s", baseDisp.Prompt)
	}

	// A jig home with no recorded line, then one whose only recording is off the head's history.
	steps := []struct {
		name  string
		lines []journal.Line
	}{
		{"no recorded line", nil},
		{"an off-branch recording", []journal.Line{recordBuild(t, d, "JIG-1", l.s1, "a-a1-f0", []recSpec{{File: "side.svg", Content: "side at s1"}})}},
	}
	for _, step := range steps {
		journalLines(t, st, "JIG-1", step.lines...)
		disp, reviewJSON := dispatchedRound(t, l.roundInput(st, jigHome))
		if disp.Prompt != baseDisp.Prompt {
			t.Errorf("%s: the prompt changed:\n%s\nwant\n%s", step.name, disp.Prompt, baseDisp.Prompt)
		}
		if string(reviewJSON) != string(baseJSON) {
			t.Errorf("%s: review.json changed:\n%s\nwant\n%s", step.name, reviewJSON, baseJSON)
		}
	}
	if _, err := os.Stat(reviewListPath(t, st, jigHome, 1)); err == nil {
		t.Error("a round with no recordings wrote a recordings.json")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(reviewListPath(t, st, jigHome, 1)))); err == nil {
		t.Error("a round with no recordings made a reviews directory under the jig home")
	}
}

// TestReviewerRoundListsOnlyTheLatestRecordingsAndChecksNoMoreThanThat: a
// build with more recordings than a reviewer is handed lists the latest ones
// and counts the rest, and only the listed ones are checked against their
// files: a file past the cut that has since gone is not noticed (it is counted
// as it was), while one inside the cut that has gone is simply not listed.
func TestReviewerRoundListsOnlyTheLatestRecordingsAndChecksNoMoreThanThat(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}

	specs := func(prefix string, n int) []recSpec {
		var out []recSpec
		for i := 0; i < n; i++ {
			out = append(out, recSpec{File: fmt.Sprintf("%s-%02d.svg", prefix, i), Content: fmt.Sprintf("%s %d", prefix, i)})
		}
		return out
	}
	const early, late = 30, 30
	journalLines(t, st, "JIG-1",
		recordBuild(t, d, "JIG-1", l.c1, "a-a1-f0", specs("early", early)),
		recordBuild(t, d, "JIG-1", l.c2, "b-a1-f0", specs("late", late)),
	)

	dispatchedRound(t, l.roundInput(st, jigHome))
	list := recordedList(t, reviewListPath(t, st, jigHome, 1))
	if len(list.Recordings) != maxReviewRecordings || list.Omitted != early+late-maxReviewRecordings {
		t.Fatalf("listed %d recordings, omitted %d; want %d listed and %d omitted", len(list.Recordings), list.Omitted, maxReviewRecordings, early+late-maxReviewRecordings)
	}
	for i, r := range list.Recordings {
		want := l.c1
		if i < late {
			want = l.c2
		}
		if r.Commit != want {
			t.Errorf("recording %d is from %s, want %s: the latest build's come first", i, r.Commit, want)
		}
	}

	// early-05 is the sixth of the early build's, inside the cut; early-25 is past it.
	for _, name := range []string{"early-05.svg", "early-25.svg"} {
		if err := os.Remove(recordedFile(t, d, "JIG-1", l.c1, "a-a1-f0", name)); err != nil {
			t.Fatal(err)
		}
	}
	in := l.roundInput(st, jigHome)
	in.Round = 2
	dispatchedRound(t, in)
	list = recordedList(t, reviewListPath(t, st, jigHome, 2))
	if len(list.Recordings) != maxReviewRecordings-1 || list.Omitted != early+late-maxReviewRecordings {
		t.Errorf("after two files went: listed %d, omitted %d; want %d listed (the one inside the cut gone) and %d omitted (the cut as it was)", len(list.Recordings), list.Omitted, maxReviewRecordings-1, early+late-maxReviewRecordings)
	}
}

// TestReviewerRoundLeavesOutARecordingTheReadToolsWouldRefuse: a recording
// whose name the screen that governs a reviewer's reads would deny as a
// credential (".env*", "*_key*") is not listed, and is counted as omitted
// instead of being listed as evidence the reviewer cannot read.
func TestReviewerRoundLeavesOutARecordingTheReadToolsWouldRefuse(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}
	journalLines(t, st, "JIG-1", recordBuild(t, d, "JIG-1", l.c2, "a-a1-f0", []recSpec{
		{File: "home.svg", Content: "home"},
		{File: "press_key.svg", Content: "a key press"},
		{File: ".env-setup.svg", Content: "setup"},
	}))

	dispatchedRound(t, l.roundInput(st, jigHome))
	list := recordedList(t, reviewListPath(t, st, jigHome, 1))
	if len(list.Recordings) != 1 || list.Recordings[0].Scenario != "home" || list.Omitted != 2 {
		t.Errorf("listed %+v, omitted %d; want home alone, with the two the reads would refuse counted", list.Recordings, list.Omitted)
	}
}

// TestReviewerRoundNeverWritesThroughALinkOrReadsAnEarlierList: the list is not
// written below a directory that is a link (the round fails and the link's
// target is untouched), a link in the list's own place is replaced, not written
// through, and a list left by an earlier attempt at the round is replaced.
func TestReviewerRoundNeverWritesThroughALinkOrReadsAnEarlierList(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}
	journalLines(t, st, "JIG-1", recordBuild(t, d, "JIG-1", l.c2, "a-a1-f0", []recSpec{{File: "home.svg", Content: "home"}}))
	path := reviewListPath(t, st, jigHome, 1)
	outside := t.TempDir()

	// A round directory that is a link: refused, and nothing lands in its target.
	linkDir(t, filepath.Dir(path), outside)
	backend := stubBackend{run: func(session.Dispatch) error {
		t.Error("the reviewer was dispatched after the list could not be written")
		return nil
	}}
	_, _, err := NewReviewerGateSource(backend).Round(l.roundInput(st, jigHome))
	if err == nil || !strings.Contains(err.Error(), "recordings.json") {
		t.Fatalf("Round through a linked round directory: err = %v, want a refusal naming recordings.json", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the list was written through the link: %v", entries)
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}

	// A list of an earlier attempt, and a link in the list's own place.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("an earlier attempt's list"), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatchedRound(t, l.roundInput(st, jigHome))
	if list := recordedList(t, path); len(list.Recordings) != 1 {
		t.Errorf("the earlier attempt's list was not replaced: %+v", list)
	}

	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("file links unavailable here: %v", err)
	}
	dispatchedRound(t, l.roundInput(st, jigHome))
	if got, _ := os.ReadFile(target); string(got) != "outside" {
		t.Errorf("the list was written through a link: %q", got)
	}
	if list := recordedList(t, path); len(list.Recordings) != 1 {
		t.Errorf("the link was not replaced by the list: %+v", list)
	}
}

// hostSpellingsIn reports which spellings of dir (as the machine's paths are
// scrubbed, the WSL mount among them) s still holds.
func hostSpellingsIn(s, dir string) []string {
	var found []string
	for _, sp := range hostPathSpellings(true, hostDir{dir, "x"}) {
		if strings.Contains(s, sp.text) {
			found = append(found, sp.text)
		}
	}
	return found
}

// TestReviewerRoundLeavesTheMachinesPathsOutOfWhatTheReviewerWrote: a finding
// that cites a recording by its path, in any of the spellings the path has,
// reaches Gate without the jig home in it, in every free-text field and in the
// summary, and so does the result.json the reviewer left in the store's work
// directory; the lease and store paths go the same way. The files the result
// names are not text and still count: an absolute reviewed path inside the
// lease is relativized as before.
func TestReviewerRoundLeavesTheMachinesPathsOutOfWhatTheReviewerWrote(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}
	journalLines(t, st, "JIG-1", recordBuild(t, d, "JIG-1", l.c2, "a-a1-f0", []recSpec{{File: "home.svg", Content: "home"}}))
	rec := recordedFile(t, d, "JIG-1", l.c2, "a-a1-f0", "home.svg")

	var resultPath string
	backend := stubBackend{run: func(sd session.Dispatch) error {
		resultPath = sd.ResultJSON
		result := ReviewResult{
			Findings: []ResultFinding{{
				File: "a.go", Line: 1, Action: ActionNote, Risk: RiskLow,
				Title:         "the screen in " + rec + " is wrong",
				Detail:        "see " + filepath.ToSlash(rec) + " and " + fmt.Sprintf("%q", rec) + ", checked in " + l.dir,
				RiskRationale: "from " + session.WSLPath(rec),
			}},
			ReviewedPaths: []string{filepath.Join(l.dir, "a.go"), "b.go"},
			Summary:       "read " + jigHome + " and " + st.Root,
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	rnd, ok, err := NewReviewerGateSource(backend).Round(l.roundInput(st, jigHome))
	if err != nil || !ok {
		t.Fatalf("Round: ok = %v, err = %v", ok, err)
	}
	res := rnd.Review.Result
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	texts := map[string]string{"title": f.Title, "detail": f.Detail, "risk_rationale": f.RiskRationale, "summary": res.Summary}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	texts["result.json"] = string(raw)
	for name, text := range texts {
		for _, dir := range []string{jigHome, l.dir, st.Root} {
			if found := hostSpellingsIn(text, dir); len(found) > 0 {
				t.Errorf("%s still holds %v:\n%s", name, found, text)
			}
		}
	}
	if !strings.Contains(f.Title, "<jig home>") || !strings.Contains(f.Detail, "<lease>") || !strings.Contains(res.Summary, "<store>") {
		t.Errorf("the paths were not replaced by their names: title %q, detail %q, summary %q", f.Title, f.Detail, res.Summary)
	}
	if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(res.ReviewedPaths, want) {
		t.Errorf("reviewed_paths = %v, want %v: an absolute path inside the lease still counts as coverage", res.ReviewedPaths, want)
	}
}

// TestGateCommitsNoPathOfTheJigHomeWhenAFindingCitesARecording: through Gate, a
// reviewer that cites a recording by its absolute path leaves the finding in
// the round's files, and nothing in the store's working copy (what the round
// commits and pushes) names the jig home.
func TestGateCommitsNoPathOfTheJigHomeWhenAFindingCitesARecording(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	tip, err := gitx.RevParse(buildLeaseDir(t, fx), ticketBranch(fx.Ticket))
	if err != nil {
		t.Fatal(err)
	}
	journalLines(t, d.Store, fx.Ticket, recordBuild(t, d, fx.Ticket, tip, "a-a1-f0", []recSpec{{File: "login.svg", Content: "login"}}))
	rec := recordedFile(t, d, fx.Ticket, tip, "a-a1-f0", "login.svg")

	backend := stubBackend{run: func(sd session.Dispatch) error {
		req := readReviewRequest(t, sd.SliceJSON)
		if !strings.Contains(sd.Prompt, "recordings.json") {
			t.Errorf("the reviewer was not told of the recordings:\n%s", sd.Prompt)
		}
		result := ReviewResult{
			Findings: []ResultFinding{{
				File: req.MustReview[0], Line: 1, Action: ActionNote, Risk: RiskLow,
				Title: "the login screen is wrong", Detail: "the recording at " + rec + " shows it", RiskRationale: "read from " + rec,
			}},
			ReviewedPaths: req.MustReview,
			Summary:       "looked at " + rec,
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	if _, err := Gate(d, NewReviewerGateSource(backend), GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate: %v", err)
	}

	findings, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "findings.yaml"))
	if err != nil {
		t.Fatalf("read findings.yaml: %v", err)
	}
	if !strings.Contains(string(findings), "the login screen is wrong") || !strings.Contains(string(findings), "<jig home>") {
		t.Errorf("the finding is not in findings.yaml, with the path replaced by its name:\n%s", findings)
	}
	err = filepath.WalkDir(d.Store.Root, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if de.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if found := hostSpellingsIn(string(data), d.Home); len(found) > 0 {
			t.Errorf("%s names the jig home (%v)", p, found)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestReviewerRoundWritesAListOfItsOwn: each round writes its own list, so a
// later round never reads an earlier round's.
func TestReviewerRoundWritesAListOfItsOwn(t *testing.T) {
	t.Parallel()
	l := newRecordingsLease(t)
	st := newReviewStore(t)
	jigHome := t.TempDir()
	d := Deps{Store: st, Home: jigHome}
	journalLines(t, st, "JIG-1", recordBuild(t, d, "JIG-1", l.c2, "a-a1-f0", []recSpec{{File: "home.svg", Content: "home"}}))

	for round := 1; round <= 2; round++ {
		in := l.roundInput(st, jigHome)
		in.Round = round
		disp, _ := dispatchedRound(t, in)
		if want := reviewListPath(t, st, jigHome, round); !strings.Contains(disp.Prompt, "recordings.json at "+want+" lists") {
			t.Errorf("round %d's prompt does not name %s:\n%s", round, want, disp.Prompt)
		}
		recordedList(t, reviewListPath(t, st, jigHome, round))
	}
}
