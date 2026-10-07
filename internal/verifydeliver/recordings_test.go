package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	// The reviewer is given no new place to write for this.
	if disp.ExtraWriteDir != "" {
		t.Errorf("ExtraWriteDir = %q, want none", disp.ExtraWriteDir)
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

// TestReviewerRoundListsOnlyTheLatestRecordingsAndSaysHowManyItLeftOut: a
// build with more recordings than a reviewer is handed lists the latest ones
// and counts the rest.
func TestReviewerRoundListsOnlyTheLatestRecordingsAndSaysHowManyItLeftOut(t *testing.T) {
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

	disp, _ := dispatchedRound(t, l.roundInput(st, jigHome))
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
	if !strings.Contains(disp.Prompt, "omitted") {
		t.Errorf("the prompt does not tell the reviewer that the list can be cut:\n%s", disp.Prompt)
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
