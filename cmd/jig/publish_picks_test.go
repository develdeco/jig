package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPublishPicksTheBuildsRecordingsThroughMain drives a clean gate round,
// journals a recording the build made, and publishes with the fake backend
// answering the pick: the pull request's ## Demo section is the pick's and the
// report says what was picked. It is the CLI half of ADR 0029's publish step:
// the flags, the backend, the scenario's publish/picks-result.json.
func TestPublishPicksTheBuildsRecordingsThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "clean-round", Home: jigHome})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	if out, code := runMain(t, e, "", gate...); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate: exit = %d, want 0 and a clean verdict\n%s", code, out)
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

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
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

// TestPublishWithoutRecordingsHasNoDemoSectionThroughMain: a ticket whose
// build recorded nothing publishes a pull request body with no ## Demo
// section, and no session is dispatched to pick from nothing (the scenario
// scripts no pick result, so one would fail loudly).
func TestPublishWithoutRecordingsHasNoDemoSectionThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "clean-round", Home: jigHome})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	if out, code := runMain(t, e, "", gate...); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate: exit = %d, want 0 and a clean verdict\n%s", code, out)
	}
	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
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

// TestPublishBackendFailsOnlyWhenUsed: a backend that is not installed does
// not stop a publish that has no recordings to pick from, and a backend name
// jig does not have is refused at once. The availability check is the
// caller's, so the branch of a backend that is not there is reached here with
// one that is missing.
func TestPublishBackendFailsOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	gone := &axi.Error{Msg: "the headless backend needs claude, which is not on PATH", Code: "BACKEND_UNAVAILABLE"}
	missing := func(string) error { return gone }
	present := func(string) error { return nil }

	if _, err := publishBackend("nonesuch", "", present); err == nil {
		t.Error("an unknown backend name was accepted")
	}
	if _, err := publishBackend("nonesuch", "", missing); err == nil {
		t.Error("an unknown backend name was accepted because its program was not checked first")
	}

	b, err := publishBackend("headless", "", missing)
	if err != nil {
		t.Fatalf("a backend that is not installed stopped publish: %v", err)
	}
	if _, ok := b.(unavailableBackend); !ok {
		t.Fatalf("publishBackend(headless, missing) = %T, want an unavailableBackend", b)
	}
	var ae *axi.Error
	if err := b.Run(session.Dispatch{}); !errors.As(err, &ae) || ae.Code != "BACKEND_UNAVAILABLE" {
		t.Errorf("an unavailable backend ran with %v, want it to fail with BACKEND_UNAVAILABLE", err)
	}

	b, err = publishBackend("fake", t.TempDir(), present)
	if err != nil {
		t.Fatalf("publishBackend(fake): %v", err)
	}
	if _, ok := b.(unavailableBackend); ok || b == nil {
		t.Errorf("publishBackend(fake) = %T, want the fake backend", b)
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
