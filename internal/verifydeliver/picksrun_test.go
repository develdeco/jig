package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

// picksSpy is the agent that picks, as a publish test sees it: a picks file
// holding the answer it gives, verbatim. jig starts no session to pick with,
// so a publish is handed the file (opts) or none, and the spy counts how many
// were handed one.
type picksSpy struct {
	path   string
	handed int
}

// opts is o with the spy's picks file, as `jig publish --picks` gives it.
func (s *picksSpy) opts(o PublishOpts) PublishOpts {
	s.handed++
	o.Picks = s.path
	return o
}

// picksScenario writes result, verbatim, as a picks file and returns the spy
// that hands it to a publish.
func picksScenario(t *testing.T, result string) *picksSpy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "picks.json")
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
	return &picksSpy{path: path}
}

// onboardingPick answers the candidates onboardingRecordings makes: r1 is the
// lone dashboard video, r2 and r3 the two steps of the onboarding flow.
const onboardingPick = `{"flows":[` +
	`{"title":"Onboarding","items":[{"id":"r2"},{"id":"r3","caption":"profile saved"}]},` +
	`{"title":"Dashboard","items":[{"id":"r1"}]}],` +
	`"summary":"Two flows show the change."}`

// picksFixture is a ticket built and gated clean, with a fake gh to publish
// to, ready for a build's recordings.
type picksFixture struct {
	fx       *fixture.Fixture
	d        Deps
	head     string   // the head the gate reviewed, which publish ships
	built    []string // the commits jig built
	logFile  string
	warnings []string
}

func newPicksFixture(t *testing.T) *picksFixture {
	t.Helper()
	pf := &picksFixture{}
	pf.fx = fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	pf.d = newDeps(t, pf.fx)
	pf.d.Warn = func(format string, args ...any) { pf.warnings = append(pf.warnings, fmt.Sprintf(format, args...)) }
	gateCleanReviewerRound(t, pf.fx, pf.d)
	lines, err := journal.Read(pf.d.Store, pf.fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	pf.built = journal.BuiltCommits(lines)
	if len(pf.built) < 2 {
		t.Fatalf("built commits = %v, want at least two", pf.built)
	}
	last, err := existingGateRounds(pf.d.Store, pf.fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	pf.head = reportYAMLAt(t, pf.d, pf.fx.Ticket, last).ReviewedSHA["fixture-repo"]
	if pf.head == "" {
		t.Fatal("the clean round recorded no reviewed head")
	}
	pf.logFile, _ = useGithubHost(t, &pf.d, "", "")
	return pf
}

// record writes specs as the recordings of one oracle run at commit and
// appends the recorded line that describes them.
func (pf *picksFixture) record(t *testing.T, commit, run string, specs ...recSpec) {
	t.Helper()
	line := recordBuild(t, pf.d, pf.fx.Ticket, commit, run, specs)
	if err := journal.Append(pf.d.Store, pf.fx.Ticket, line); err != nil {
		t.Fatal(err)
	}
}

// onboardingRecordings records what the scenarios of a small build wrote: a
// lone dashboard video and, in two runs at two commits, the two steps of an
// onboarding flow.
func (pf *picksFixture) onboardingRecordings(t *testing.T) {
	t.Helper()
	pf.record(t, pf.built[0], "a-a1-f0",
		recSpec{File: "onboard-2.svg", Content: "profile svg", Scenario: "profile", Flow: "onboard", Step: 2},
		recSpec{File: "lone.mp4", Content: "dashboard video bytes", Scenario: "dashboard"})
	pf.record(t, pf.built[len(pf.built)-1], "d-a1-f0",
		recSpec{File: "onboard-1.png", Content: "login png", Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in"})
}

// picksDir is where the picked files of the head the gate reviewed are staged.
func (pf *picksFixture) picksDir(t *testing.T) string {
	t.Helper()
	id, err := pf.d.Store.ID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := home.PicksDir(pf.d.Home, id, pf.fx.Ticket, pf.head)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// pickLines are the ticket's publish-picks journal lines.
func (pf *picksFixture) pickLines(t *testing.T) []journal.Line {
	t.Helper()
	lines, err := journal.Read(pf.d.Store, pf.fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == "publish-picks" {
			out = append(out, l)
		}
	}
	return out
}

// body is the pull request body publish wrote for report.
func (pf *picksFixture) body(t *testing.T, report PublishReport) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pf.d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	if err != nil {
		t.Fatalf("read the pr body: %v", err)
	}
	return string(data)
}

func (pf *picksFixture) warned(substr string) bool {
	for _, w := range pf.warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestPublishRendersAndAttachesThePickedRecordings: an agent answers with flows
// in a picks file; publish renders the ## Demo section from them, flow by flow
// in the order picked, stages the files under names of its own and attaches
// them through the host, and journals the pick.
func TestPublishRendersAndAttachesThePickedRecordings(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	// A recording whose file is gone is no candidate: it is named in the output.
	pf.record(t, pf.built[0], "a-a1-f1", recSpec{File: "ghost.svg", Content: "ghost"})
	if err := os.Remove(recordedFile(t, pf.d, pf.fx.Ticket, pf.built[0], "a-a1-f1", "ghost.svg")); err != nil {
		t.Fatal(err)
	}
	spy := picksScenario(t, onboardingPick)

	report, err := Publish(pf.d, spy.opts(PublishOpts{Ticket: pf.fx.Ticket, Yes: true}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// The report, the journal and the output say what was picked and dropped.
	if report.Picks.Status != PicksPicked || report.Picks.Flows != 2 || report.Picks.Files != 3 ||
		!reflect.DeepEqual(report.Picks.Dropped, []string{"ghost.svg (gone)"}) {
		t.Errorf("report.Picks = %+v, want picked, 2 flows, 3 files, and ghost.svg dropped", report.Picks)
	}
	if !pf.warned("ghost.svg (gone)") {
		t.Errorf("warnings = %q, want the dropped recording named", pf.warnings)
	}
	picks := pf.pickLines(t)
	if len(picks) != 1 || picks[0].Outcome != PicksPicked || picks[0].Commit != pf.head || picks[0].Pick == nil {
		t.Fatalf("publish-picks lines = %+v, want one picked line for the reviewed head", picks)
	}
	wantFlows := []journal.PickFlow{
		{Title: "Onboarding", Items: []journal.PickItem{{ID: "r2", File: "onboard-1.png"}, {ID: "r3", File: "onboard-2.svg", Caption: "profile saved"}}},
		{Title: "Dashboard", Items: []journal.PickItem{{ID: "r1", File: "lone.mp4"}}},
	}
	if p := picks[0].Pick; p.Summary != "Two flows show the change." || p.Candidates == "" || !reflect.DeepEqual(p.Flows, wantFlows) {
		t.Errorf("journaled pick = %+v, want the summary, a fingerprint and the flows %+v", p, wantFlows)
	}

	// The files are staged under names of jig's own, in flow order, and the
	// host attaches that directory's files, in that order.
	dir := pf.picksDir(t)
	for name, want := range map[string]string{"rec-1.png": "login png", "rec-2.svg": "profile svg", "rec-3.mp4": "dashboard video bytes"} {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != want {
			t.Errorf("staged %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 3 {
		t.Errorf("staging directory holds %d entries (err %v), want exactly the 3 picked files", len(entries), err)
	}
	create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create")
	if create == nil {
		t.Fatal("no logged pr create call")
	}
	if !sameDir(t, create.Dir, dir) {
		t.Errorf("pr create ran in %q, want the staging directory %q", create.Dir, dir)
	}
	if got, want := attachedFiles(create.Argv), []string{"rec-1.png", "rec-2.svg", "rec-3.mp4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pr create --attach files = %v, want %v", got, want)
	}

	// The section: the summary, then each flow under its heading with its
	// recordings in the form gh rewrites for their kind (images in place, the
	// video patched from the read-back).
	body := pf.body(t, report)
	want := "## Demo\n\nTwo flows show the change.\n\n" +
		"### Onboarding\n\n" +
		"- ![signs in](https://github.example/user-attachments/assets/1)\n" +
		"- ![profile saved](https://github.example/user-attachments/assets/2)\n\n" +
		"### Dashboard\n\n" +
		"- https://github.example/user-attachments/assets/3: dashboard\n\n"
	if !strings.Contains(body, want) {
		t.Errorf("pr body = %q\nwant it to hold %q", body, want)
	}
	if strings.Contains(body, "./rec-") {
		t.Errorf("pr body = %q, want no dead relative path left", body)
	}
	// The section sits between What changed and Verification, in that order.
	whatChanged, demo, verification := strings.Index(body, "## What changed"), strings.Index(body, "## Demo"), strings.Index(body, "## Verification")
	if whatChanged < 0 || !(whatChanged < demo && demo < verification) {
		t.Errorf("pr body sections out of order (What changed=%d, Demo=%d, Verification=%d):\n%s", whatChanged, demo, verification, body)
	}
	if pf.warned("no demo for") {
		t.Errorf("warnings = %q, want no word about a missing demo when a pick stands", pf.warnings)
	}
}

// TestPublishLeavesOutTheDemoSectionWhenThePickIsRefused: a pick jig refuses
// (an id that is no candidate, one used twice, an answer that is not JSON) is
// journaled with jig's reason and said in the output, and the pull request
// carries no ## Demo section and attaches nothing.
func TestPublishLeavesOutTheDemoSectionWhenThePickIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, result, reason string }{
		{"an id that is no candidate", `{"flows":[{"title":"T","items":[{"id":"r9"}]}],"summary":"S"}`, "flow 1 item 1 is not a candidate"},
		{"an id picked twice", `{"flows":[{"title":"T","items":[{"id":"r1"}]},{"title":"U","items":[{"id":"r1"}]}],"summary":"S"}`, "flow 2 item 1 is a candidate that is already picked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pf := newPicksFixture(t)
			pf.onboardingRecordings(t)
			spy := picksScenario(t, c.result)

			report, err := Publish(pf.d, spy.opts(PublishOpts{Ticket: pf.fx.Ticket, Yes: true}))
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}

			if report.Picks.Status != PicksRefused || !strings.Contains(report.Picks.Reason, c.reason) {
				t.Errorf("report.Picks = %+v, want a refusal naming %q", report.Picks, c.reason)
			}
			picks := pf.pickLines(t)
			if len(picks) != 1 || picks[0].Pick != nil || !strings.HasPrefix(picks[0].Outcome, "refused: ") || !strings.Contains(picks[0].Outcome, c.reason) {
				t.Errorf("publish-picks lines = %+v, want one refused line with the reason and no pick", picks)
			}
			if !pf.warned("no recordings were picked") || !pf.warned(c.reason) {
				t.Errorf("warnings = %q, want the refusal and its reason in the output", pf.warnings)
			}
			if pf.warned("no demo for") {
				t.Errorf("warnings = %q, want the refusal said once, not again as a missing demo", pf.warnings)
			}

			if body := pf.body(t, report); strings.Contains(body, "## Demo") || strings.Contains(body, "rec-") {
				t.Errorf("pr body = %q, want no Demo section", body)
			}
			create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create")
			if create == nil {
				t.Fatal("no logged pr create call")
			}
			if got := attachedFiles(create.Argv); len(got) != 0 {
				t.Errorf("pr create attached %v, want nothing", got)
			}
		})
	}
}

// TestPublishWithNoRecordingsOffersNoPick: a ticket whose build recorded
// nothing publishes as it always did: nothing to pick, no request written, no
// journal line, no flows.
func TestPublishWithNoRecordingsOffersNoPick(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	// Lines that are not candidates do not make a pick either: a recording of a
	// commit the branch does not hold, and a recorded line that was refused.
	pf.record(t, strings.Repeat("a", 40), "z-a1-f0", recSpec{File: "off.svg", Content: "off"})
	refused := journal.Line{Slice: "a", Event: "recorded", Commit: pf.built[0], RecordRun: "a-a1-f1", Outcome: "refused: a tag is not valid"}
	if err := journal.Append(pf.d.Store, pf.fx.Ticket, refused); err != nil {
		t.Fatal(err)
	}

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !reflect.DeepEqual(report.Picks, PicksReport{}) {
		t.Errorf("report.Picks = %+v, want the zero value", report.Picks)
	}
	if lines := pf.pickLines(t); len(lines) != 0 {
		t.Errorf("publish-picks lines = %+v, want none", lines)
	}
	if body := pf.body(t, report); strings.Contains(body, "### ") || strings.Contains(body, "rec-") {
		t.Errorf("pr body = %q, want no flows", body)
	}
	if create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create"); create == nil || len(attachedFiles(create.Argv)) != 0 {
		t.Errorf("pr create = %+v, want one that attaches nothing", create)
	}
	// Nothing was recorded that could be shown or left out, and the output
	// says that, in those words.
	if !pf.warned("jig: no demo for "+pf.fx.Ticket+": there are no recordings to show") || pf.warned("left out of the choice") {
		t.Errorf("warnings = %q, want the one line saying there are no recordings to show", pf.warnings)
	}
}

// TestPublishSaysNoRecordingIsLeftWhenEveryOneWasDropped: a build that did
// record, whose every file no longer matches its line, is told apart from one
// that recorded nothing: the output names what was left out and then says none
// is left, never that there were none to show. Nothing is offered to pick
// from, and the pull request has no ## Demo section.
func TestPublishSaysNoRecordingIsLeftWhenEveryOneWasDropped(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.record(t, pf.built[0], "a-a1-f0", recSpec{File: "ghost.svg", Content: "ghost"})
	if err := os.Remove(recordedFile(t, pf.d, pf.fx.Ticket, pf.built[0], "a-a1-f0", "ghost.svg")); err != nil {
		t.Fatal(err)
	}

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !reflect.DeepEqual(report.Picks.Dropped, []string{"ghost.svg (gone)"}) || report.Picks.Status != "" {
		t.Errorf("report.Picks = %+v, want ghost.svg dropped and no pick", report.Picks)
	}
	if !pf.warned("recordings left out of the choice for "+pf.fx.Ticket+": ghost.svg (gone)") ||
		!pf.warned("jig: no demo for "+pf.fx.Ticket+": no recordings are left to show") ||
		pf.warned("there are no recordings to show") {
		t.Errorf("warnings = %q, want the dropped file named and then none left, never none to show", pf.warnings)
	}
	if body := pf.body(t, report); strings.Contains(body, "## Demo") {
		t.Errorf("pr body = %q, want no Demo section", body)
	}
}

// TestPublishReusesAPickForTheSameHeadAndRecordings: a publish that stops
// before it ships (a declined confirmation) and runs again needs no picks file
// the second time, the pick being the same question; recordings that changed
// in between are a new question, and the publish is pending again until it is
// given a file.
func TestPublishReusesAPickForTheSameHeadAndRecordings(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	pf.d.Confirm = func(string, string, string, bool) bool { return false }

	_, err := Publish(pf.d, spy.opts(PublishOpts{Ticket: pf.fx.Ticket}))
	wantAxiCode(t, err, "PUBLISH_DECLINED")
	_, err = Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket})
	wantAxiCode(t, err, "PUBLISH_DECLINED")
	picks := pf.pickLines(t)
	if len(picks) != 2 || picks[0].Outcome != PicksPicked || picks[1].Outcome != PicksReused || !reflect.DeepEqual(picks[0].Pick, picks[1].Pick) {
		t.Fatalf("publish-picks lines = %+v, want picked then reused, the same pick", picks)
	}

	// A new recording changes the candidates: the earlier pick answers another
	// question, so none is reused and the publish is pending again.
	pf.record(t, pf.built[0], "a-a2-f0", recSpec{File: "extra.svg", Content: "extra", Scenario: "extra", Flow: "extra", Step: 1})
	pf.d.Confirm = nil
	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Picks.Status != PicksPending || report.Picks.Candidates != 4 {
		t.Errorf("report.Picks = %+v after the recordings changed; want a pending pick offering the 4 candidates there now", report.Picks)
	}
}

// TestPublishWithRecordingsAndNoPickWritesTheRequestAnAgentPicksFrom: recordings
// but nobody has picked: jig picks nothing and asks no model. It writes the
// picks.json an agent picks from - the ticket, the head, the intent and the
// candidates in a stable order - says so in the output and the report, and
// publishes without a demo; a later publish given the answer shows it.
func TestPublishWithRecordingsAndNoPickWritesTheRequestAnAgentPicksFrom(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Picks.Status != PicksPending || report.Picks.Candidates != 3 || report.Picks.Request == "" {
		t.Fatalf("report.Picks = %+v, want a pending pick offering 3 candidates and naming the request", report.Picks)
	}
	if want := pf.picksDir(t) + ".picks.json"; report.Picks.Request != want {
		t.Errorf("request = %q, want it beside the staging directory: %q", report.Picks.Request, want)
	}
	var req PicksRequest
	data, err := os.ReadFile(report.Picks.Request)
	if err != nil {
		t.Fatalf("read the request: %v", err)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse the request: %v", err)
	}
	wantCands := []PicksCandidate{
		{ID: "r1", Scenario: "dashboard", Commit: pf.built[0], Kind: "video", Name: "lone.mp4"},
		{ID: "r2", Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in", Commit: pf.built[len(pf.built)-1], Kind: "image", Name: "onboard-1.png"},
		{ID: "r3", Scenario: "profile", Flow: "onboard", Step: 2, Commit: pf.built[0], Kind: "image", Name: "onboard-2.svg"},
	}
	if req.Ticket != pf.fx.Ticket || req.HeadSHA != pf.head || req.Intent.Source != IntentSourceBrief || !reflect.DeepEqual(req.Candidates, wantCands) {
		t.Errorf("picks.json = %+v\nwant ticket %s, head %s, the brief, candidates %+v", req, pf.fx.Ticket, pf.head, wantCands)
	}
	if !pf.warned("waiting to be picked") || !pf.warned(report.Picks.Request) {
		t.Errorf("warnings = %q, want the output to say recordings wait to be picked, and where", pf.warnings)
	}
	if lines := pf.pickLines(t); len(lines) != 0 {
		t.Errorf("publish-picks lines = %+v, want none: nothing was picked or refused", lines)
	}
	if body := pf.body(t, report); strings.Contains(body, "## Demo") {
		t.Errorf("pr body = %q, want no Demo section until a pick is given", body)
	}
	if create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create"); create == nil || len(attachedFiles(create.Argv)) != 0 {
		t.Errorf("pr create = %+v, want one that attaches nothing", create)
	}
}

// TestPublishRefusesAnUnreadableOrInvalidPicksFile: a picks file jig cannot
// read, or that is not a pick result at all, stops the publish before it writes
// or pushes anything: the agent gave jig a file, and a publish that went on
// without it would hide the mistake. A pick that parses and is then refused
// (an id that is no candidate) is another matter, journaled and published
// without a demo (TestPublishLeavesOutTheDemoSectionWhenThePickIsRefused).
func TestPublishRefusesAnUnreadableOrInvalidPicksFile(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	invalid := picksScenario(t, `{"flows": "none"}`)

	for name, opts := range map[string]PublishOpts{
		"unreadable": {Ticket: pf.fx.Ticket, Yes: true, Picks: filepath.Join(t.TempDir(), "missing.json")},
		"invalid":    invalid.opts(PublishOpts{Ticket: pf.fx.Ticket, Yes: true}),
	} {
		_, err := Publish(pf.d, opts)
		want := "PICKS_UNREADABLE"
		if name == "invalid" {
			want = "PICKS_INVALID"
		}
		wantAxiCode(t, err, want)
	}
	if sha := originRef(t, pf.fx.RepoRemote, "refs/heads/"+ticketBranch(pf.fx.Ticket)); sha != "" {
		t.Errorf("origin has the ticket branch at %s after the refused publishes, want nothing pushed", sha)
	}
	if lines := pf.pickLines(t); len(lines) != 0 {
		t.Errorf("publish-picks lines = %+v, want none", lines)
	}
}

// step is the pick step for the fixture's ticket as it stands: its journal read
// now, the build lease (which holds the commits jig built) as the lease the
// recorded commits are looked up in, and the head the gate reviewed. result is
// the pick an agent made, nil for none.
func (pf *picksFixture) step(t *testing.T, result *PicksResult) picksStep {
	t.Helper()
	lines, err := journal.Read(pf.d.Store, pf.fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	return picksStep{
		d: pf.d, result: result, ticket: pf.fx.Ticket, repoName: "fixture-repo",
		leaseDir: buildLeaseDir(t, pf.fx), head: pf.head, lines: lines, warn: pf.d.Warn,
	}
}

// pickOf parses answer, a picks file's text, as a pick result.
func pickOf(t *testing.T, answer string) *PicksResult {
	t.Helper()
	res, err := ParsePicksResult([]byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	return &res
}

// onboardingPicks resolves the answers the staging tests stage: all three of
// the onboarding recordings, and only the first step of the onboarding flow.
func onboardingPicks(t *testing.T, cands []pickCandidate) (three, one pick) {
	t.Helper()
	res, err := ParsePicksResult([]byte(onboardingPick))
	if err != nil {
		t.Fatal(err)
	}
	if three, err = resolvePicks(res, cands); err != nil {
		t.Fatal(err)
	}
	if one, err = resolvePicks(PicksResult{Summary: "S", Flows: []PicksFlow{{Title: "T", Items: []PicksItem{{ID: "r2"}}}}}, cands); err != nil {
		t.Fatal(err)
	}
	return three, one
}

// dirNames are the names of the entries of dir, in order.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestPublishRefusesStagedMediaThatChangedBeforeThePush: the pick's files are
// checked once more after the confirmation and before anything is pushed. A
// file edited or swapped for a link since it was staged stops the publish with
// a code, with origin untouched and nothing uploaded; the next publish takes
// the pick from the journal, stages the recordings afresh, and ships them, with
// no gate round in between.
func TestPublishRefusesStagedMediaThatChangedBeforeThePush(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(t *testing.T, staged string)
	}{
		{"rewritten to the same size", func(t *testing.T, staged string) {
			if err := os.WriteFile(staged, []byte("LOGIN PNG"), 0o644); err != nil {
				t.Errorf("rewrite the staged file: %v", err)
			}
		}},
		{"replaced by a link", func(t *testing.T, staged string) {
			target := filepath.Join(t.TempDir(), "elsewhere.png")
			if err := os.WriteFile(target, []byte("login png"), 0o644); err != nil {
				t.Errorf("write the link's target: %v", err)
				return
			}
			if err := os.Remove(staged); err != nil {
				t.Errorf("remove the staged file: %v", err)
				return
			}
			if err := os.Symlink(target, staged); err != nil {
				t.Skipf("cannot make a symbolic link here: %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pf := newPicksFixture(t)
			pf.onboardingRecordings(t)
			spy := picksScenario(t, onboardingPick)
			staged := filepath.Join(pf.picksDir(t), "rec-1.png")
			pushed := false
			pf.d.GuardedPush = func(string, string, string, bool) error {
				pushed = true
				return nil
			}
			// The confirmation is where the time passes: it runs after the pick
			// is staged and before the check.
			pf.d.Confirm = func(string, string, string, bool) bool {
				c.change(t, staged)
				return true
			}

			_, err := Publish(pf.d, spy.opts(PublishOpts{Ticket: pf.fx.Ticket}))
			wantAxiCode(t, err, "PUBLISH_PICKS_CHANGED")
			var ae *axi.Error
			if errors.As(err, &ae) && !strings.Contains(strings.Join(ae.Help, " "), "Nothing was pushed") {
				t.Errorf("help = %q, want it to say nothing was pushed", ae.Help)
			}
			if pushed {
				t.Error("the branch was pushed although the staged media had changed")
			}
			if sha := originRef(t, pf.fx.RepoRemote, "refs/heads/"+ticketBranch(pf.fx.Ticket)); sha != "" {
				t.Errorf("origin has the ticket branch at %s after a refused publish, want it untouched", sha)
			}
			if create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create"); create != nil {
				t.Errorf("pr create ran (%v) after the staged media changed", create.Argv)
			}

			// The next publish needs no picks file, stages afresh, and ships.
			pf.d.Confirm, pf.d.GuardedPush = nil, nil
			report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
			if err != nil {
				t.Fatalf("the publish after the refusal: %v", err)
			}
			if report.Picks.Status != PicksReused || spy.handed != 1 {
				t.Errorf("status = %q after the file was given %d time(s), want the pick reused after one", report.Picks.Status, spy.handed)
			}
			if got, err := os.ReadFile(staged); err != nil || string(got) != "login png" {
				t.Errorf("staged rec-1.png = %q, %v; want the recording staged afresh", got, err)
			}
			create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create")
			if create == nil {
				t.Fatal("no pr create call after the second publish")
			}
			if got, want := attachedFiles(create.Argv), []string{"rec-1.png", "rec-2.svg", "rec-3.mp4"}; !reflect.DeepEqual(got, want) {
				t.Errorf("pr create --attach files = %v, want %v", got, want)
			}
			if sha := originRef(t, pf.fx.RepoRemote, "refs/heads/"+ticketBranch(pf.fx.Ticket)); sha == "" {
				t.Error("the second publish pushed nothing")
			}
		})
	}
}

// TestRemoveStagingNeverFollowsALink: the cleanup of a failed staging goes by
// path, so it removes the directory only when a directory above it is still
// plain and it is still the directory stage made; a swapped one is left, and
// what a link leads to is never removed.
func TestRemoveStagingNeverFollowsALink(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (top, dir string, made os.FileInfo) {
		t.Helper()
		top = filepath.Join(t.TempDir(), "evidence")
		dir = filepath.Join(top, "id", "T-1", "picks", "head")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rec-1.png"), []byte("staged"), 0o644); err != nil {
			t.Fatal(err)
		}
		made, err := media.LstatPinned(dir)
		if err != nil {
			t.Fatal(err)
		}
		return top, dir, made
	}
	// victim is a directory outside the evidence tree that a link leads to.
	victim := func(t *testing.T) string {
		t.Helper()
		v := t.TempDir()
		if err := os.WriteFile(filepath.Join(v, "precious"), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		return v
	}
	kept := func(t *testing.T, v string) {
		t.Helper()
		if got, err := os.ReadFile(filepath.Join(v, "precious")); err != nil || string(got) != "keep" {
			t.Errorf("what the link leads to = %q, %v; want it untouched", got, err)
		}
	}

	t.Run("the directory it made", func(t *testing.T) {
		t.Parallel()
		top, dir, made := setup(t)
		removeStaging(top, dir, made)
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("the staging directory is still there (err %v)", err)
		}
	})
	t.Run("the directory swapped for a link", func(t *testing.T) {
		t.Parallel()
		top, dir, made := setup(t)
		v := victim(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, v, dir)
		removeStaging(top, dir, made)
		kept(t, v)
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("the swapped directory was removed (err %v), want it left for the operator", err)
		}
	})
	t.Run("a directory above it swapped for a link", func(t *testing.T) {
		t.Parallel()
		top, dir, made := setup(t)
		v := victim(t)
		if err := os.MkdirAll(filepath.Join(v, "head"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v, "head", "rec-1.png"), []byte("not ours"), 0o644); err != nil {
			t.Fatal(err)
		}
		picks := filepath.Dir(dir)
		if err := os.Rename(picks, picks+".moved"); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, v, picks)
		removeStaging(top, dir, made)
		kept(t, v)
		if got, err := os.ReadFile(filepath.Join(v, "head", "rec-1.png")); err != nil || string(got) != "not ours" {
			t.Errorf("a file behind the link = %q, %v; want it untouched", got, err)
		}
	})
	t.Run("a directory it never pinned that is a link", func(t *testing.T) {
		t.Parallel()
		top, dir, _ := setup(t)
		v := victim(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, v, dir)
		removeStaging(top, dir, nil)
		kept(t, v)
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("the link itself is still there (err %v), want it removed as itself", err)
		}
	})
}

// TestAPickIsNotReusedAfterTheIntentChanged: the intent a pick was judged
// against is part of the question. A new gate round on the same head can
// change it (a brief edited, an explicit intent set), and a pick made for the
// old one is then asked again, not reused.
func TestAPickIsNotReusedAfterTheIntentChanged(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	run := func(result *PicksResult) PicksReport {
		t.Helper()
		report, _, err := publishPicks(pf.step(t, result))
		if err != nil {
			t.Fatalf("publishPicks: %v", err)
		}
		return report
	}

	if got := run(pickOf(t, onboardingPick)).Status; got != PicksPicked {
		t.Fatalf("first status = %q, want picked", got)
	}
	if got := run(nil).Status; got != PicksReused {
		t.Fatalf("second status = %q, want the pick reused without a picks file", got)
	}
	brief := filepath.Join(pf.d.Store.TicketDir(pf.fx.Ticket), "brief.md")
	text, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, append(text, []byte("\nA paragraph added after the first pick.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(nil).Status; got != PicksPending {
		t.Errorf("third status = %q, want a pending pick: the earlier one answered another intent", got)
	}
	if got := run(pickOf(t, onboardingPick)).Status; got != PicksPicked {
		t.Errorf("fourth status = %q, want a new pick from a new picks file", got)
	}
}

// TestStagingLeavesNothingOfAnEarlierPickOfTheSameHead: a pick of fewer files
// than the one before it stages exactly its own, so no rec-N of the earlier
// pick is left to be attached, and a pick whose staging fails leaves no
// directory at all.
func TestStagingLeavesNothingOfAnEarlierPickOfTheSameHead(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	s := pf.step(t, nil)
	cands, _, err := pickCandidates(s.d, s.ticket, s.lines, s.leaseDir, s.head)
	if err != nil {
		t.Fatal(err)
	}
	three, one := onboardingPicks(t, cands)
	dir := s.picksDir()
	top := absPath(filepath.Join(pf.d.Home, "evidence"))

	if files, err := s.stage(dir, three); err != nil || len(files) != 3 {
		t.Fatalf("staging three: %v, %v", files, err)
	}
	files, err := s.stage(dir, one)
	if err != nil || len(files) != 1 {
		t.Fatalf("staging one: %v, %v", files, err)
	}
	if got := dirNames(t, dir); !reflect.DeepEqual(got, []string{"rec-1.png"}) {
		t.Errorf("staging directory holds %v after the smaller pick, want only rec-1.png", got)
	}
	if err := verifyStaged(top, dir, files); err != nil {
		t.Errorf("verifyStaged on the staged files: %v", err)
	}

	// A file that changed since it was offered refuses the pick and leaves
	// no half-staged directory behind.
	lone := recordedFile(t, pf.d, pf.fx.Ticket, pf.built[0], "a-a1-f0", "lone.mp4")
	if err := os.WriteFile(lone, []byte("DASHBOARD VIDEO BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.stage(dir, three); err == nil || !strings.Contains(err.Error(), "the recording lone.mp4 changed while it was being staged") {
		t.Errorf("staging a changed recording: err = %v, want it refused", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused staging left the directory behind (err %v)", err)
	}
}

// TestStagingNeverWritesThroughALink: a link where the staging directory goes
// is removed as itself and replaced by a real directory, and its target gets
// nothing; a link above it (the ticket's picks directory) refuses the pick
// before anything is written.
func TestStagingNeverWritesThroughALink(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	s := pf.step(t, nil)
	cands, _, err := pickCandidates(s.d, s.ticket, s.lines, s.leaseDir, s.head)
	if err != nil {
		t.Fatal(err)
	}
	_, one := onboardingPicks(t, cands)
	dir := s.picksDir()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	symlinkOrSkip(t, target, dir)
	if _, err := s.stage(dir, one); err != nil {
		t.Fatalf("staging over a link: %v", err)
	}
	if got := dirNames(t, target); len(got) != 0 {
		t.Errorf("the link's target received %v, want nothing", got)
	}
	if fi, err := os.Lstat(dir); err != nil || fi.Mode().Type() != os.ModeDir {
		t.Errorf("the staging directory is %v (err %v), want a real directory", fi, err)
	}

	if err := os.RemoveAll(filepath.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	above := t.TempDir()
	symlinkOrSkip(t, above, filepath.Dir(dir))
	report, rendered, err := publishPicks(pf.step(t, pickOf(t, onboardingPick)))
	if err != nil {
		t.Fatalf("publishPicks: %v", err)
	}
	if report.Status != PicksRefused || !strings.Contains(report.Reason, "not a plain directory") || rendered != nil {
		t.Errorf("report = %+v, rendered = %v; want a refusal because a directory above is a link", report, rendered)
	}
	if got := dirNames(t, above); len(got) != 0 {
		t.Errorf("the link's target received %v, want nothing", got)
	}
}
