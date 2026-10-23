package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// gateCleanThroughMain takes the fixture ticket, built by buildFixtureTicket,
// through one recorded gate round the way an agent does: `jig gate` opens it,
// `jig review context` reads it, and `jig review done --result` closes it with
// a report that found nothing and read every file the request names.
func gateCleanThroughMain(t *testing.T, e env, fx *fixture.Fixture) {
	t.Helper()
	flags := []string{"--store", fx.StoreDir}
	if out, code := runMain(t, e, "", append([]string{"gate", fx.Ticket}, flags...)...); code != 0 {
		t.Fatalf("gate: exit = %d, want 0\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", append([]string{"review", "context", fx.Ticket, "--json"}, flags...)...)
	if code != 0 {
		t.Fatalf("review context: exit = %d, want 0\n%s", code, ctx)
	}
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json is not the request: %v\n%s", err, ctx)
	}
	result, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": req.MustReview, "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	if err := os.WriteFile(path, result, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, e, "", append([]string{"review", "done", fx.Ticket, "--result", path}, flags...)...)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("review done: exit = %d, want 0 and a clean verdict\n%s", code, out)
	}
}

// recordGreeting journals one recording the build made at its first verified
// commit, a small svg tagged with a scenario and a caption, and returns the
// store it was journaled in.
func recordGreeting(t *testing.T, jigHome string, fx *fixture.Fixture) *store.Store {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	built := journal.BuiltCommits(lines)
	if len(built) == 0 {
		t.Fatal("the build journaled no verified commit")
	}
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	recDir, err := home.RecordDir(jigHome, id, fx.Ticket, built[0], "a-a1-f0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(recDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"/>`
	if err := os.WriteFile(filepath.Join(recDir, "greet.svg"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	if err := journal.Append(st, fx.Ticket, journal.Line{
		Slice: "a", Event: "recorded", Commit: built[0], Attempt: 1, RecordRun: "a-a1-f0",
		Recordings: []journal.Recording{{File: "greet.svg", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), Scenario: "greet", Caption: "Greet reads casual"}},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestPublishPicksTheBuildsRecordingsThroughMain drives a clean gate round,
// journals a recording the build made, and publishes with the picks file an
// agent wrote: the pull request's ## Demo section is the pick's and the report
// says what was picked. It is the CLI half of ADR 0029's publish step: the
// --picks flag, the file's strict parse, the report.
func TestPublishPicksTheBuildsRecordingsThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	st := recordGreeting(t, jigHome, fx)

	answer := filepath.Join(t.TempDir(), "picks.json")
	if err := os.WriteFile(answer, []byte(`{"flows":[{"title":"Greeting","items":[{"id":"r1"}]}],"summary":"The greeting reads casual."}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--picks", answer, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish: exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"picks: picked", "picks_flows: 1", "picks_files: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("publish output lacks %q:\n%s", want, out)
		}
	}

	body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil {
		t.Fatalf("read pr/fixture-repo.md: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"## Demo\n\nThe greeting reads casual.\n\n### Greeting\n\n- ![Greet reads casual](./rec-1.svg)\n\n## Verification",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pr body lacks %q:\n%s", want, text)
		}
	}
}

// TestPublishBodyOffersTheRecordingsToPickFromThroughMain: `jig publish --body`
// for a build that recorded something, with no picks file, writes the
// picks.json an agent picks from and says where it is; the same command given
// the answer renders the demo. Nothing is pushed by either.
func TestPublishBodyOffersTheRecordingsToPickFromThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	st := recordGreeting(t, jigHome, fx)

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--body", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish --body: exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"picks: pending", "picks_candidates: 1", "picks_request: "} {
		if !strings.Contains(out, want) {
			t.Errorf("publish --body output lacks %q:\n%s", want, out)
		}
	}
	var request string
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "picks_request: "); ok {
			request = strings.Trim(v, `"`)
		}
	}
	data, err := os.ReadFile(request)
	if err != nil || !strings.Contains(string(data), `"name": "greet.svg"`) {
		t.Fatalf("the request %q = %q (err %v), want the candidate greet.svg", request, data, err)
	}
	if body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md")); err != nil || strings.Contains(string(body), "## Demo") {
		t.Errorf("pr body = %q (err %v), want one with no demo until a pick is given", body, err)
	}

	answer := filepath.Join(t.TempDir(), "picks.json")
	if err := os.WriteFile(answer, []byte(`{"flows":[{"title":"Greeting","items":[{"id":"r1"}]}],"summary":"The greeting reads casual."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "publish", fx.Ticket, "--body", "--picks", answer, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "picks: picked") {
		t.Fatalf("publish --body --picks: exit = %d, want 0 and a pick\n%s", code, out)
	}
	if body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md")); err != nil || !strings.Contains(string(body), "### Greeting") {
		t.Errorf("pr body = %q (err %v), want the demo section", body, err)
	}
	if got, err := gitx.Run(fx.RepoRemote, "for-each-ref", "--format=%(objectname)", "refs/heads/jig/"+fx.Ticket); err != nil || got != "" {
		t.Errorf("origin has the ticket branch at %q (err %v) after --body, want nothing pushed", got, err)
	}
}

// TestPublishWithoutRecordingsHasNoDemoSectionThroughMain: a ticket whose
// build recorded nothing publishes a pull request body with no ## Demo
// section, and nothing is offered to pick from.
func TestPublishWithoutRecordingsHasNoDemoSectionThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish: exit = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "picks:") {
		t.Errorf("publish output reports a pick for a build that recorded nothing:\n%s", out)
	}

	body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil {
		t.Fatalf("read pr/fixture-repo.md: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "## Demo") {
		t.Errorf("pr body has a ## Demo section although nothing was recorded:\n%s", text)
	}
	for _, want := range []string{"## What changed", "## Verification"} {
		if !strings.Contains(text, want) {
			t.Errorf("pr body lacks %q:\n%s", want, text)
		}
	}
}

// TestPicksRowsSayWhatWasPickedOrWhy: none for a build that recorded nothing,
// and for one that did the status and the one thing each status adds.
func TestPicksRowsSayWhatWasPickedOrWhy(t *testing.T) {
	t.Parallel()
	if rows := picksRows(verifydeliver.PicksReport{}); rows != nil {
		t.Errorf("rows for no recordings = %v, want none", rows)
	}
	cases := []struct {
		report verifydeliver.PicksReport
		want   [][2]string
	}{
		{verifydeliver.PicksReport{Status: verifydeliver.PicksPicked, Flows: 2, Files: 3},
			[][2]string{{"picks", "picked"}, {"picks_flows", "2"}, {"picks_files", "3"}}},
		{verifydeliver.PicksReport{Status: verifydeliver.PicksReused, Flows: 1, Files: 1, Dropped: []string{"a.svg (gone)", "b.svg (changed)"}},
			[][2]string{{"picks", "reused"}, {"picks_flows", "1"}, {"picks_files", "1"}, {"picks_dropped", "a.svg (gone); b.svg (changed)"}}},
		{verifydeliver.PicksReport{Status: verifydeliver.PicksRefused, Reason: "it picked no recording"},
			[][2]string{{"picks", "refused"}, {"picks_reason", "it picked no recording"}}},
		{verifydeliver.PicksReport{Status: verifydeliver.PicksPending, Candidates: 3, Request: "/home/picks.json"},
			[][2]string{{"picks", "pending"}, {"picks_candidates", "3"}, {"picks_request", "/home/picks.json"}}},
		{verifydeliver.PicksReport{Dropped: []string{"a.svg (gone)"}},
			[][2]string{{"picks_dropped", "a.svg (gone)"}}},
	}
	for _, c := range cases {
		got := picksRows(c.report)
		if len(got) != len(c.want) {
			t.Errorf("picksRows(%+v) = %v, want %v", c.report, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("picksRows(%+v)[%d] = %v, want %v", c.report, i, got[i], c.want[i])
			}
		}
	}
}
