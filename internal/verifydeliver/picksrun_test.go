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
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
)

// picksSpy is the session backend a publish test hands Publish: it records
// every dispatch and the picks.json it was handed, then plays the scenario
// back through the real fake backend, or fails like a backend that could not
// run when err is set. during, when set, runs after the request is read and
// before the session answers, as a session working in the meantime would.
type picksSpy struct {
	inner      session.Backend
	err        error
	during     func()
	dispatches []session.Dispatch
	requests   []PicksRequest
	scratch    []bool // whether each dispatch's working directory existed while it ran
}

func (s *picksSpy) Run(d session.Dispatch) error {
	s.dispatches = append(s.dispatches, d)
	var req PicksRequest
	if data, err := os.ReadFile(d.SliceJSON); err == nil {
		_ = json.Unmarshal(data, &req)
	}
	s.requests = append(s.requests, req)
	fi, err := os.Stat(d.Worktree)
	s.scratch = append(s.scratch, err == nil && fi.IsDir())
	if s.during != nil {
		s.during()
	}
	if s.err != nil {
		return s.err
	}
	return s.inner.Run(d)
}

// picksScenario makes a fake-backend scenario whose publish picks answer is
// result, verbatim, and returns a spy that plays it back.
func picksScenario(t *testing.T, result string) *picksSpy {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "publish"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "publish", "picks-result.json"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
	backend, err := session.New("fake", session.Options{ScenarioDir: dir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	return &picksSpy{inner: backend}
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

// TestPublishRendersAndAttachesThePickedRecordings: a short session is handed
// the candidates and answers with flows; publish renders the ## Demo section
// from them, flow by flow in the order picked, stages the files under names of
// its own and attaches them through the host exactly as it attaches a gate
// demo's, and journals the pick.
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

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// One short session, on the cheapest rung at low effort, in a scratch
	// directory that is gone again, handed the candidates in a stable order.
	if len(spy.dispatches) != 1 {
		t.Fatalf("dispatches = %d, want one", len(spy.dispatches))
	}
	disp := spy.dispatches[0]
	if disp.Slice != session.PublishPicksSlice || disp.Model != "rung-a" || disp.Effort != "low" || !disp.NoSessionPersistence || !disp.Screen {
		t.Errorf("dispatch = slice %q model %q effort %q persistence-off %v screen %v; want the publish picks slice on rung-a at low effort, screened, keeping no transcript",
			disp.Slice, disp.Model, disp.Effort, disp.NoSessionPersistence, disp.Screen)
	}
	if !spy.scratch[0] {
		t.Error("the session's working directory did not exist while it ran")
	}
	if _, err := os.Stat(disp.Worktree); !os.IsNotExist(err) {
		t.Errorf("the scratch directory %s outlived the session (err %v)", disp.Worktree, err)
	}
	if !strings.Contains(disp.Prompt, disp.SliceJSON) || !strings.Contains(disp.Prompt, disp.ResultJSON) {
		t.Errorf("prompt = %q, want it to name picks.json and the result", disp.Prompt)
	}
	req := spy.requests[0]
	wantCands := []PicksCandidate{
		{ID: "r1", Scenario: "dashboard", Commit: pf.built[0], Kind: "video", Name: "lone.mp4"},
		{ID: "r2", Scenario: "login", Flow: "onboard", Step: 1, Caption: "signs in", Commit: pf.built[len(pf.built)-1], Kind: "image", Name: "onboard-1.png"},
		{ID: "r3", Scenario: "profile", Flow: "onboard", Step: 2, Commit: pf.built[0], Kind: "image", Name: "onboard-2.svg"},
	}
	if req.Ticket != pf.fx.Ticket || req.HeadSHA != pf.head || req.Intent.Source != IntentSourceBrief || !reflect.DeepEqual(req.Candidates, wantCands) {
		t.Errorf("picks.json = %+v\nwant ticket %s, head %s, the brief, candidates %+v", req, pf.fx.Ticket, pf.head, wantCands)
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
}

// TestPublishFallsBackToTheGateDemoWhenThePickIsRefused: a pick jig refuses
// (an id that is no candidate, one used twice, an answer that is not JSON) is
// journaled with jig's reason and said in the output, and the gate's demo
// renders and attaches as it always did.
func TestPublishFallsBackToTheGateDemoWhenThePickIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, result, reason string }{
		{"an id that is no candidate", `{"flows":[{"title":"T","items":[{"id":"r9"}]}],"summary":"S"}`, "flow 1 item 1 is not a candidate"},
		{"an id picked twice", `{"flows":[{"title":"T","items":[{"id":"r1"}]},{"title":"U","items":[{"id":"r1"}]}],"summary":"S"}`, "flow 2 item 1 is a candidate that is already picked"},
		{"an answer that is not JSON", `I think r1 shows it.`, "it must contain exactly one JSON object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pf := newPicksFixture(t)
			pf.onboardingRecordings(t)
			mediaDir, files := recordDemoForTicket(t, pf.d, pf.fx.Ticket, []demoMediaSpec{{Name: "demo-1.png", Content: "the gate demo"}})
			spy := picksScenario(t, c.result)

			report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
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

			body := pf.body(t, report)
			if !strings.Contains(body, "it works") || !strings.Contains(body, "![caption for demo-1.png](./demo-1.png)") || strings.Contains(body, "### ") {
				t.Errorf("pr body = %q, want the gate demo's section and no flows", body)
			}
			create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create")
			if create == nil {
				t.Fatal("no logged pr create call")
			}
			if !sameDir(t, create.Dir, mediaDir) || !reflect.DeepEqual(attachedFiles(create.Argv), []string{files[0].Name}) {
				t.Errorf("pr create ran in %q attaching %v, want the gate demo's directory %q and %s", create.Dir, attachedFiles(create.Argv), mediaDir, files[0].Name)
			}
		})
	}
}

// TestPublishWithNoRecordingsDispatchesNoPick: a ticket whose build recorded
// nothing publishes as it always did: no session, no journal line, no flows.
func TestPublishWithNoRecordingsDispatchesNoPick(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	spy := &picksSpy{err: fmt.Errorf("a pick was dispatched")}
	// Lines that are not candidates do not make a pick either: a recording of a
	// commit the branch does not hold, and a recorded line that was refused.
	pf.record(t, strings.Repeat("a", 40), "z-a1-f0", recSpec{File: "off.svg", Content: "off"})
	refused := journal.Line{Slice: "a", Event: "recorded", Commit: pf.built[0], RecordRun: "a-a1-f1", Outcome: "refused: a tag is not valid"}
	if err := journal.Append(pf.d.Store, pf.fx.Ticket, refused); err != nil {
		t.Fatal(err)
	}

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(spy.dispatches) != 0 {
		t.Errorf("%d pick sessions were dispatched for a build with nothing to pick from", len(spy.dispatches))
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
}

// TestPublishReusesAPickForTheSameHeadAndRecordings: a publish that stops
// before it ships (a declined confirmation) and runs again asks the session
// nothing the second time; recordings that changed in between are a new
// question.
func TestPublishReusesAPickForTheSameHeadAndRecordings(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	pf.d.Confirm = func(string, string, string, bool) bool { return false }

	for i := 0; i < 2; i++ {
		_, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Backend: spy})
		wantAxiCode(t, err, "PUBLISH_DECLINED")
	}
	if len(spy.dispatches) != 1 {
		t.Fatalf("dispatches = %d after two publishes of the same head and recordings, want one", len(spy.dispatches))
	}
	picks := pf.pickLines(t)
	if len(picks) != 2 || picks[0].Outcome != PicksPicked || picks[1].Outcome != PicksReused || !reflect.DeepEqual(picks[0].Pick, picks[1].Pick) {
		t.Fatalf("publish-picks lines = %+v, want picked then reused, the same pick", picks)
	}

	// A new recording changes the candidates: the pick is made again.
	pf.record(t, pf.built[0], "a-a2-f0", recSpec{File: "extra.svg", Content: "extra", Scenario: "extra", Flow: "extra", Step: 1})
	pf.d.Confirm = nil
	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(spy.dispatches) != 2 || report.Picks.Status != PicksPicked {
		t.Errorf("dispatches = %d, status = %q after the recordings changed; want a second session and a new pick", len(spy.dispatches), report.Picks.Status)
	}
	if got := len(spy.requests[1].Candidates); got != 4 {
		t.Errorf("the second session was offered %d candidates, want the 4 there now", got)
	}
}

// TestPublishWithNoBackendRefusesThePick: recordings but no session to pick
// with is a refusal in the output and the journal, not a failed publish.
func TestPublishWithNoBackendRefusesThePick(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Picks.Status != PicksRefused || !strings.Contains(report.Picks.Reason, "no session backend") {
		t.Errorf("report.Picks = %+v, want a refusal for want of a backend", report.Picks)
	}
	if body := pf.body(t, report); strings.Contains(body, "### ") {
		t.Errorf("pr body = %q, want no flows", body)
	}
}

// TestPublishRecordsAFailedPickSessionByItsCodeOnly: the text of a failed
// session or backend can hold anything, so the journal and the report carry
// only its failure code, and the output carries the text once.
func TestPublishRecordsAFailedPickSessionByItsCodeOnly(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := &picksSpy{err: fmt.Errorf("the backend failed in %s", pf.d.Home)}

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if want := "the pick session failed: INTERNAL"; report.Picks.Status != PicksRefused || report.Picks.Reason != want {
		t.Errorf("report.Picks = %+v, want a refusal with the reason %q", report.Picks, want)
	}
	picks := pf.pickLines(t)
	if len(picks) != 1 || picks[0].Outcome != "refused: the pick session failed: INTERNAL" {
		t.Errorf("publish-picks lines = %+v, want one refusal carrying the code alone", picks)
	}
	if !pf.warned("the backend failed in") {
		t.Errorf("warnings = %q, want the failure's text printed", pf.warnings)
	}
}

// TestPublishRefusesAPickWhoseFileChangedAfterItWasOffered: the session runs
// for a while, and a file it was offered can change meanwhile. Each file is
// checked again as it is staged, and a pick holding one that changed is refused
// whole, so nothing unchecked reaches the pull request.
func TestPublishRefusesAPickWhoseFileChangedAfterItWasOffered(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	lone := recordedFile(t, pf.d, pf.fx.Ticket, pf.built[0], "a-a1-f0", "lone.mp4")
	spy.during = func() {
		// The same length, so only the bytes tell.
		if err := os.WriteFile(lone, []byte("DASHBOARD VIDEO BYTES"), 0o644); err != nil {
			t.Errorf("rewrite the recording: %v", err)
		}
	}

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Picks.Status != PicksRefused || !strings.Contains(report.Picks.Reason, "the recording lone.mp4 changed while it was being staged") {
		t.Errorf("report.Picks = %+v, want a refusal naming the file that changed", report.Picks)
	}
	if create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create"); create == nil || len(attachedFiles(create.Argv)) != 0 {
		t.Errorf("pr create = %+v, want one that attaches nothing", create)
	}
	if body := pf.body(t, report); strings.Contains(body, "### ") {
		t.Errorf("pr body = %q, want no flows", body)
	}
}
