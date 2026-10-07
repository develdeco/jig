package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPublishPicksTheBuildsRecordingsThroughMain drives a clean gate round
// whose scenario has a gate demo, journals a recording the build made, and
// publishes with the fake backend answering the pick: the pull request's
// ## Demo section is the pick's, the gate demo's files are not in it, and the
// report says what was picked. It is the CLI half of ADR 0029's publish step:
// the flags, the backend, the scenario's publish/picks-result.json.
func TestPublishPicksTheBuildsRecordingsThroughMain(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "demo"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	if out, code := runMain(t, "", gate...); code != 0 || !strings.Contains(out, "verdict: clean") || !strings.Contains(out, "demo: recorded") {
		t.Fatalf("gate: exit = %d, want 0 with a recorded demo\n%s", code, out)
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

	if err := os.MkdirAll(filepath.Join(fx.ScenarioDir, "publish"), 0o755); err != nil {
		t.Fatal(err)
	}
	answer := `{"flows":[{"title":"Greeting","items":[{"id":"r1"}]}],"summary":"The greeting reads casual."}`
	if err := os.WriteFile(filepath.Join(fx.ScenarioDir, "publish", "picks-result.json"), []byte(answer), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, "", "publish", fx.Ticket, "--yes", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
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
	if strings.Contains(text, "demo-1.svg") {
		t.Errorf("pr body still carries the gate demo's files:\n%s", text)
	}
}

// TestPublishBackendFailsOnlyWhenUsed: a backend that is not installed does
// not stop a publish that has no recordings to pick from, and a backend name
// jig does not have is refused at once.
func TestPublishBackendFailsOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	if _, err := publishBackend("nonesuch", ""); err == nil {
		t.Error("an unknown backend name was accepted")
	}
	if b, err := publishBackend("fake", t.TempDir()); err != nil || b == nil {
		t.Errorf("publishBackend(fake) = %v, %v; want the fake backend", b, err)
	}
	gone := errors.New("the backend needs a program that is not on PATH")
	if err := (unavailableBackend{gone}).Run(session.Dispatch{}); !errors.Is(err, gone) {
		t.Errorf("an unavailable backend ran with %v, want it to fail with why it is unavailable", err)
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
