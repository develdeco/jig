package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// The tests here keep the rules of a round on the path production takes: a
// recorded round (GatePrepare, a result the test writes, GateFinish) or a direct
// call of the function that does the job, never a reviewer session jig starts.

// recordedRound runs one round the way an agent jig did not start runs it: the
// round is opened (GatePrepare), reviewer says what it found of what it was
// handed, and the round is closed (GateFinish). A round that opened carried or
// queued fix slices has no reviewer, and its report comes back as it is.
func recordedRound(t *testing.T, d Deps, o GateOpts, reviewer func(p Prepared) ReviewResult) GateReport {
	t.Helper()
	p, err := GatePrepare(d, o)
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried != nil {
		return *p.Carried
	}
	report, err := GateFinish(d, p, reviewer(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag})
	if err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	return report
}

// preparedRequest is the request a round over in hands its reviewer, and what
// PrepareReviewRequest wrote of it.
func preparedRequest(t *testing.T, in RoundInput) ReviewPrep {
	t.Helper()
	prep, err := PrepareReviewRequest(in)
	if err != nil {
		t.Fatalf("PrepareReviewRequest: %v", err)
	}
	return prep
}

// TestRecordedRoundKeepsTheRecordingsOfTheHeadsHistory: a round whose head's
// history holds recordings writes a recordings.json under the jig home (not in
// the store) listing the latest recording of each flow, scenario and step made
// at the head or an ancestor, latest first, each with the absolute path of its
// file.
func TestRecordedRoundKeepsTheRecordingsOfTheHeadsHistory(t *testing.T) {
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

	prep := preparedRequest(t, l.roundInput(st, jigHome))

	path := reviewListPath(t, st, jigHome, 1)
	if prep.RecordingsPath != path {
		t.Errorf("RecordingsPath = %q, want %q", prep.RecordingsPath, path)
	}
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
	reviewJSON, err := os.ReadFile(prep.RequestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reviewJSON), "recordings") {
		t.Errorf("review.json mentions the recordings; they are a file of their own:\n%s", reviewJSON)
	}
}

// TestRecordedRoundKeepsOnlyTheLatestRecordings: a list is cut to the latest
// maxReviewRecordings, counts the rest as omitted, and checks no more than the
// ones it lists.
func TestRecordedRoundKeepsOnlyTheLatestRecordings(t *testing.T) {
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

	preparedRequest(t, l.roundInput(st, jigHome))
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
	preparedRequest(t, in)
	list = recordedList(t, reviewListPath(t, st, jigHome, 2))
	if len(list.Recordings) != maxReviewRecordings-1 || list.Omitted != early+late-maxReviewRecordings {
		t.Errorf("after two files went: listed %d, omitted %d; want %d listed (the one inside the cut gone) and %d omitted (the cut as it was)", len(list.Recordings), list.Omitted, maxReviewRecordings-1, early+late-maxReviewRecordings)
	}
}

// TestRecordedRoundKeepsARecordingItsReaderRefusesOutOfTheList: a recording the round's
// Readable rule refuses is not listed, and is counted as omitted instead of
// being listed as evidence the reviewer cannot read.
func TestRecordedRoundKeepsARecordingItsReaderRefusesOutOfTheList(t *testing.T) {
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

	in := l.roundInput(st, jigHome)
	in.Readable = func(file string) bool {
		base := filepath.Base(file)
		return !strings.HasPrefix(base, ".env") && !strings.Contains(base, "_key")
	}
	preparedRequest(t, in)
	list := recordedList(t, reviewListPath(t, st, jigHome, 1))
	if len(list.Recordings) != 1 || list.Recordings[0].Scenario != "home" || list.Omitted != 2 {
		t.Errorf("listed %+v, omitted %d; want home alone, with the two the reader refuses counted", list.Recordings, list.Omitted)
	}
}

// TestRecordedRoundKeepsTheListFromBeingWrittenThroughALink: the list is not
// written below a directory that is a link (the round fails and the link's
// target is untouched), a link in the list's own place is replaced, not written
// through, and a list left by an earlier attempt at the round is replaced.
func TestRecordedRoundKeepsTheListFromBeingWrittenThroughALink(t *testing.T) {
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
	_, err := PrepareReviewRequest(l.roundInput(st, jigHome))
	if err == nil || !strings.Contains(err.Error(), "recordings.json") {
		t.Fatalf("PrepareReviewRequest through a linked round directory: err = %v, want a refusal naming recordings.json", err)
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
	preparedRequest(t, l.roundInput(st, jigHome))
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
	preparedRequest(t, l.roundInput(st, jigHome))
	if got, _ := os.ReadFile(target); string(got) != "outside" {
		t.Errorf("the list was written through a link: %q", got)
	}
	if list := recordedList(t, path); len(list.Recordings) != 1 {
		t.Errorf("the link was not replaced by the list: %+v", list)
	}
}

// TestRecordedRoundKeepsItsProbeLedger: a probe a round reports is kept with the
// blob of the file it probed, and the next round is handed it: current while the
// file stands as it was, stale once the branch changed it, and a skip stays a
// skip of its hunks. The ledger a reviewer is handed names the probes the
// reviewer had no part in writing.
func TestRecordedRoundKeepsItsProbeLedger(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)

	var reqs []ReviewRequest
	var alpha, beta HunkEntry
	skipped := ""
	reviewer := func(p Prepared) ReviewResult {
		reqs = append(reqs, p.Request)
		result := ReviewResult{ReviewedPaths: p.Request.MustReview, Summary: "probed"}
		if len(reqs) == 1 {
			for _, h := range p.Request.Hunks {
				switch {
				case alpha.File == "" && strings.HasPrefix(h.File, "alpha/"):
					alpha = h
				case beta.File == "" && strings.HasPrefix(h.File, "beta/"):
					beta = h
				}
			}
			for _, h := range p.Request.Hunks {
				if h.File != alpha.File && h.File != beta.File {
					skipped = h.File
				}
			}
			if alpha.File == "" || beta.File == "" || skipped == "" {
				t.Fatalf("round 1 hunks = %+v, want hunks in alpha and beta and a third file", p.Request.Hunks)
			}
			result.Probes = []ResultProbe{
				{Seam: "alpha's path", File: alpha.File, Line: alpha.From, Mutation: "negate it", Command: "go test ./alpha", Outcome: ProbeCaught},
				{Seam: "beta's path", File: beta.File, Line: beta.From, Mutation: "drop it", Command: "go test ./beta", Outcome: ProbeSurvived},
			}
			result.Skipped = []ResultSkip{{File: skipped, Why: "tests"}}
		}
		return result
	}

	report1 := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, reviewer)
	req1 := reqs[0]
	if len(req1.Probed) != 0 {
		t.Errorf("round 1 probed = %+v, want none", req1.Probed)
	}
	for _, h := range req1.Hunks {
		if !h.Unprobed() {
			t.Errorf("round 1 hunk %+v, want every hunk unprobed", h)
		}
	}
	covered := 0
	for _, h := range req1.Hunks {
		if h == alpha || h == beta || h.File == skipped {
			covered++
		}
	}
	if report1.Probes != 2 || report1.Unprobed != len(req1.Hunks)-covered {
		t.Errorf("round 1 report: probes %d, unprobed %d; want 2 and %d", report1.Probes, report1.Unprobed, len(req1.Hunks)-covered)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff1.Probes) != 2 || len(ff1.Skipped) != 1 {
		t.Fatalf("round 1 findings.yaml probes %+v, skipped %+v; want 2 and 1", ff1.Probes, ff1.Skipped)
	}
	for _, p := range ff1.Probes {
		want, err := gitx.RevParse(buildLeaseDir(t, fx), req1.HeadSHA+":"+p.File)
		if err != nil || p.Blob != want {
			t.Errorf("probe of %s keeps blob %q, want %q (err %v)", p.File, p.Blob, want, err)
		}
	}

	// The branch changes the beta file and nothing else.
	build := buildLeaseDir(t, fx)
	path := filepath.Join(build, filepath.FromSlash(beta.File))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n// changed after round 1\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	builder(t).Commit(build, "change beta after round 1")
	builder(t).Push(build, "origin", ticketBranch(fx.Ticket))

	recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, reviewer)
	if len(reqs) != 2 {
		t.Fatalf("reviewer handed %d rounds, want 2", len(reqs))
	}
	req2 := reqs[1]
	if req2.Scope != "delta" {
		t.Errorf("round 2 scope = %q, want delta", req2.Scope)
	}
	states := map[string]string{}
	for _, p := range req2.Probed {
		if p.Round != 1 {
			t.Errorf("round 2 probed %+v, want round 1", p)
		}
		states[p.File] = p.State
	}
	if states[alpha.File] != ProbeCurrent || states[beta.File] != ProbeStale {
		t.Errorf("round 2 probed states = %v, want %s current and %s stale", states, alpha.File, beta.File)
	}
	var sawAlpha bool
	for _, h := range req2.Hunks {
		switch {
		case h == HunkEntry{File: alpha.File, From: alpha.From, To: alpha.To, Probes: 1}:
			sawAlpha = true
		case h.File == skipped && h.Skip != "tests":
			t.Errorf("round 2 hunk %+v, want it set aside as tests", h)
		case h.File == beta.File && !h.Unprobed():
			t.Errorf("round 2 hunk %+v of the changed file, want it unprobed", h)
		}
	}
	if !sawAlpha {
		t.Errorf("round 2 hunks = %+v, want the whole change's, with alpha's probed hunk %+v", req2.Hunks, alpha)
	}
}
