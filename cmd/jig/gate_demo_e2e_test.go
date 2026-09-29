package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// demoYAMLMap reads a round's demo.yaml as a plain map.
func demoYAMLMap(t *testing.T, st *store.Store, ticket string, round int) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "gate", fmt.Sprintf("round-%d", round), "demo.yaml"))
	if err != nil {
		t.Fatalf("read round %d demo.yaml: %v", round, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode round %d demo.yaml: %v", round, err)
	}
	return m
}

func mapKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}

// TestGateDemoThroughMain drives one clean reviewer round through Main with
// the fake backend, exactly as demo/gate-demo.tape does: the gate report
// prints the demo line and the recorded media, demo.yaml lands in the store
// with the media's own hashes, the media themselves land under the jig home
// (never in the store), and a second round on the same head prints that its
// demo already exists instead of running another.
func TestGateDemoThroughMain(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "demo"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, "", gate...)
	if code != 0 {
		t.Fatalf("gate: exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"verdict: clean",
		"demo: recorded",
		"demo_summary: ",
		"demo_media[2]{name,bytes,caption}:",
		"demo-1.svg,",
		"demo-2.svg,",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gate report lacks %q:\n%s", want, out)
		}
	}

	// demo.yaml: exact keys, the renamed files in the order the scenario listed them.
	m := demoYAMLMap(t, st, fx.Ticket, 1)
	if got := mapKeys(m); got != "[head_sha media status summary]" {
		t.Fatalf("demo.yaml keys = %s", got)
	}
	if m["status"] != "recorded" {
		t.Fatalf("demo.yaml status = %v", m["status"])
	}
	head := m["head_sha"].(string)
	media := m["media"].([]any)
	if len(media) != 2 {
		t.Fatalf("demo.yaml lists %d files, want 2", len(media))
	}

	// The media live under the jig home, byte for byte what the scenario played back.
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	mediaDir, err := home.EvidenceDir(jigHome, id, fx.Ticket, head)
	if err != nil {
		t.Fatal(err)
	}
	for i, src := range []string{"clamp.svg", "greeting.svg"} {
		entry := media[i].(map[string]any)
		wantName := fmt.Sprintf("demo-%d.svg", i+1)
		if entry["name"] != wantName {
			t.Errorf("media %d name = %v, want %s", i, entry["name"], wantName)
		}
		want, err := os.ReadFile(filepath.Join(fx.ScenarioDir, "gate", "round-1", "demo-media", src))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(mediaDir, wantName))
		if err != nil {
			t.Fatalf("recorded media %s: %v", wantName, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the scenario's %s", wantName, src)
		}
		sum := sha256.Sum256(got)
		if entry["sha256"] != hex.EncodeToString(sum[:]) || entry["size"] != len(got) {
			t.Errorf("media %d sha256/size = %v/%v, want %x/%d", i, entry["sha256"], entry["size"], sum, len(got))
		}
	}
	// Nothing in the store, which is committed and pushed, is media or names
	// the jig home: demo.json, which holds the absolute media_dir, sits beside
	// the media.
	quoted, err := json.Marshal(jigHome) // a JSON file doubles every backslash
	if err != nil {
		t.Fatal(err)
	}
	spellings := []string{jigHome, filepath.ToSlash(jigHome), strings.Trim(string(quoted), `"`)}
	err = filepath.WalkDir(fx.StoreDir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(e.Name(), "demo-") {
			t.Errorf("media file %s is inside the store", path)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, sp := range spellings {
			if strings.Contains(string(data), sp) {
				t.Errorf("%s names the jig home %s", path, jigHome)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Round 2 on the same head: its demo exists, so it says so and runs none.
	out, code = runMain(t, "", gate...)
	if code != 0 {
		t.Fatalf("gate round 2: exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "demo: existing") || !strings.Contains(out, "demo_round: 1") || strings.Contains(out, "demo_media") {
		t.Errorf("round 2 report should name the existing round-1 demo and list no media:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir(fx.Ticket), "gate", "round-2", "demo.yaml")); err == nil {
		t.Error("round 2 wrote a demo.yaml")
	}
}

// TestGateNoDemoFlagThroughMain: --no-demo skips the demo of a clean round,
// prints no demo line, and leaves no trace of one; the next round without
// the flag then runs it.
func TestGateNoDemoFlagThroughMain(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "demo"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, "", append(append([]string{}, gate...), "--no-demo")...)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("gate --no-demo: exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "demo") {
		t.Errorf("a --no-demo report mentions a demo:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(jigHome, "evidence")); err == nil {
		t.Error("--no-demo created the evidence directory")
	}
	work := filepath.Join(st.TicketDir(fx.Ticket), "work")
	for _, p := range []string{
		filepath.Join(work, "gate.round-1.demo.result.json"),
		filepath.Join(st.TicketDir(fx.Ticket), "gate", "round-1", "demo.yaml"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("--no-demo left %s", p)
		}
	}

	// The scenario scripts a demo per round; round 2 replays round 1's.
	copyDemoRound(t, fx.ScenarioDir, 1, 2)
	out, code = runMain(t, "", gate...)
	if code != 0 || !strings.Contains(out, "demo: recorded") {
		t.Fatalf("gate without the flag: exit = %d\n%s", code, out)
	}
	if m := demoYAMLMap(t, st, fx.Ticket, 2); m["status"] != "recorded" {
		t.Errorf("round 2 demo.yaml = %v", m)
	}
}

// TestGateDemoTheScenarioDidNotScriptIsARefusalNotAFailure: the fake backend
// fails loudly on a demo the scenario has no coverage for, and the gate
// reports it as a refused demo beside a clean verdict, exit 0.
func TestGateDemoTheScenarioDidNotScriptIsARefusalNotAFailure(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "demo"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)
	if err := os.Remove(filepath.Join(fx.ScenarioDir, "gate", "round-1", "demo-result.json")); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, "", "gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("gate: exit = %d, want 0 (a refused demo never fails a round)\n%s", code, out)
	}
	// The backend's error text is the report's alone: demo.yaml, which is
	// committed to the store, records the failure's code.
	for _, want := range []string{
		"verdict: clean", "demo: refused",
		"  demo_reason: " + axi.Quote("the demo session failed: INTERNAL"),
		"  demo_detail: " + axi.Quote("session/fake: scenario has no gate round 1 demo-result.json"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gate report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "demo_media") {
		t.Errorf("a refused demo lists media:\n%s", out)
	}
	m := demoYAMLMap(t, st, fx.Ticket, 1)
	if got := mapKeys(m); got != "[head_sha reason status]" || m["status"] != "refused" {
		t.Errorf("demo.yaml = %v", m)
	}
	if m["reason"] != "the demo session failed: INTERNAL" {
		t.Errorf("demo.yaml reason = %q, want the failure's code alone", m["reason"])
	}
}

// demoSpySource is a reviewer-shaped source that reports every round clean
// and counts the demos it is asked to run, for asserting what `jig solve`
// hands Gate.
type demoSpySource struct{ demos int }

func (s *demoSpySource) Round(in verifydeliver.RoundInput) (verifydeliver.Round, bool, error) {
	head, err := gitx.RevParse(in.LeaseDir, "HEAD")
	if err != nil {
		return verifydeliver.Round{}, false, err
	}
	return verifydeliver.Round{Review: &verifydeliver.Review{
		Scope: "full", BaseSHA: head, HeadSHA: head,
		Result: verifydeliver.ReviewResult{ReviewedPaths: []string{}},
	}}, true, nil
}

func (s *demoSpySource) Demo(in verifydeliver.DemoInput) (verifydeliver.DemoResult, error) {
	s.demos++
	return verifydeliver.DemoResult{Media: []verifydeliver.DemoMedia{}, Summary: "spied"}, nil
}

// TestSolvePassesNoDemoThrough drives `jig solve` twice - each on a fresh,
// built fixture ticket - with a spying reviewer source in place of the
// reviewer session: without --no-demo the clean round runs its demo and solve
// reports it, and with --no-demo solve hands Gate the flag and none runs.
func TestSolvePassesNoDemoThrough(t *testing.T) {
	spy := &demoSpySource{}
	prev := solveGateSource
	solveGateSource = func(string, session.Backend) verifydeliver.GateSource { return spy }
	t.Cleanup(func() { solveGateSource = prev })

	for _, tc := range []struct {
		name      string
		extra     []string
		wantDemos int
		wantRow   bool
	}{
		{"with a demo", nil, 1, true},
		{"--no-demo", []string{"--no-demo"}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JIG_HOME", t.TempDir())
			fx := fixture.Generate(t, fixture.Opts{})
			buildFixtureTicket(t, fx)
			spy.demos = 0

			args := append([]string{"solve", fx.Ticket, "--yes", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}, tc.extra...)
			out, code := runMain(t, "", args...)
			if code != 0 {
				t.Fatalf("solve %v: exit = %d\n%s", tc.extra, code, out)
			}
			if spy.demos != tc.wantDemos {
				t.Errorf("demos run = %d, want %d\n%s", spy.demos, tc.wantDemos, out)
			}
			if got := strings.Contains(out, "demo: recorded"); got != tc.wantRow {
				t.Errorf("solve output has the demo row = %v, want %v:\n%s", got, tc.wantRow, out)
			}
		})
	}
}

// copyDemoRound scripts round `to`'s demo in a scenario dir as a copy of round
// `from`'s: its demo-result.json and every demo-media file.
func copyDemoRound(t *testing.T, scenarioDir string, from, to int) {
	t.Helper()
	src := filepath.Join(scenarioDir, "gate", fmt.Sprintf("round-%d", from))
	dst := filepath.Join(scenarioDir, "gate", fmt.Sprintf("round-%d", to))
	if err := os.MkdirAll(filepath.Join(dst, "demo-media"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{"demo-result.json"}
	entries, err := os.ReadDir(filepath.Join(src, "demo-media"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		files = append(files, filepath.Join("demo-media", e.Name()))
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDemoRowsShowsTheOneDetailEachStatusCarries: nothing for a round that ran
// no demo, otherwise the status, its one detail row, and a warning when the
// store push carrying demo.yaml failed.
func TestDemoRowsShowsTheOneDetailEachStatusCarries(t *testing.T) {
	cases := []struct {
		name string
		demo *verifydeliver.DemoReport
		want [][2]string
	}{
		{"no demo ran", nil, nil},
		{"recorded", &verifydeliver.DemoReport{Status: verifydeliver.DemoRecorded, Summary: "two frames"},
			[][2]string{{"demo", "recorded"}, {"demo_summary", "two frames"}}},
		{"refused", &verifydeliver.DemoReport{Status: verifydeliver.DemoRefused, Reason: "no result"},
			[][2]string{{"demo", "refused"}, {"demo_reason", "no result"}}},
		{"refused by a failure of the session", &verifydeliver.DemoReport{Status: verifydeliver.DemoRefused, Reason: "the demo session failed: INTERNAL", Detail: "boom"},
			[][2]string{{"demo", "refused"}, {"demo_reason", "the demo session failed: INTERNAL"}, {"demo_detail", "boom"}}},
		{"existing", &verifydeliver.DemoReport{Status: verifydeliver.DemoExisting, Round: 3},
			[][2]string{{"demo", "existing"}, {"demo_round", "3"}}},
		{"recorded but the push failed", &verifydeliver.DemoReport{Status: verifydeliver.DemoRecorded, Summary: "s", Warning: "push failed"},
			[][2]string{{"demo", "recorded"}, {"demo_summary", "s"}, {"demo_warning", "push failed"}}},
	}
	for _, c := range cases {
		if got := demoRows(c.demo); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: demoRows = %v, want %v", c.name, got, c.want)
		}
	}
}
