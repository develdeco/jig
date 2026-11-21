package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/project/projecttest"
)

const suiteManifestYAML = "oracles:\n  suite: \"go test ./...\"\n"

// suitePublishRig is an issue TK-1 whose one task TK-2 has landed into its
// branch, in a project whose clone holds origin/<the issue's branch> at a
// commit carrying manifest: the whole path from `jig next` to `jig publish`
// through the command's own entry point, with the fake gh as the host.
type suitePublishRig struct {
	chartRig
	tt     *testing.T
	clone  string
	branch string
}

func newSuitePublishRig(t *testing.T, manifest string) *suitePublishRig {
	t.Helper()
	r := newChartRig(t)
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  TK: everything in demo\ntrackers: []\nrepos:\n  - remote: /srv/app.git\n    label: app\n")
	r.ticket(t, "TK-1", "kind: issue\n", false)
	r.ticket(t, "TK-2", "", false)
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n", "")
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-1", URL: "https://github.example/owner/repo/pull/2"},
		{Event: journal.EventPublishDone, Into: "TK-1"},
	} {
		if err := journal.Append(r.st, "TK-2", l); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 2, Base: branch, State: "closed", Merged: true, MergeCommit: "c0ffee2"}))

	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}, {"add", "-A"}, {"commit", "-m", "store"}} {
		if _, err := gitx.Run(r.root, args...); err != nil {
			t.Fatal(err)
		}
	}
	clone := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}} {
		if _, err := gitx.Run(clone, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := projecttest.SaveMachine(r.home, map[string]project.MachineProject{"demo": {Store: r.root, Clones: map[string]string{"app": clone}, Root: clone}}); err != nil {
		t.Fatal(err)
	}
	s := &suitePublishRig{chartRig: r, tt: t, clone: clone, branch: branch}
	s.moveTip(manifest)
	return s
}

// moveTip commits manifest and points origin/<branch> at it, as a landed task
// moving the issue's branch does; it returns the new tip.
func (s *suitePublishRig) moveTip(manifest string) string {
	t := s.tt
	if manifest != "" {
		if err := os.MkdirAll(filepath.Join(s.clone, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.clone, ".claude", "jig.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--allow-empty", "-m", "tip"}} {
		if _, err := gitx.Run(s.clone, args...); err != nil {
			t.Fatal(err)
		}
	}
	tip, err := gitx.RevParse(s.clone, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(s.clone, "update-ref", "refs/remotes/origin/"+s.branch, tip); err != nil {
		t.Fatal(err)
	}
	return tip
}

func (s *suitePublishRig) record(l journal.Line) {
	s.tt.Helper()
	if err := journal.Append(s.st, "TK-1", l); err != nil {
		s.tt.Fatal(err)
	}
}

// suiteNext is `jig next TK-1 --json`: the state and the unmet codes.
func (s *suitePublishRig) next() (state string, codes []string, text string) {
	s.tt.Helper()
	out, code := s.run(s.tt, "next", "TK-1", "--json")
	var n struct {
		State string `json:"state"`
		Unmet []struct {
			Code string `json:"code"`
		} `json:"unmet"`
	}
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		s.tt.Fatalf("jig next TK-1 --json: exit %d, %v\n%s", code, err, out)
	}
	for _, u := range n.Unmet {
		codes = append(codes, u.Code)
	}
	text, _ = s.run(s.tt, "next", "TK-1")
	return n.State, codes, text
}

// body reads the pull request body the publish wrote.
func (s *suitePublishRig) body() string {
	s.tt.Helper()
	data, err := os.ReadFile(filepath.Join(s.root, "tickets", "TK-1", "pr", "app.md"))
	if err != nil {
		s.tt.Fatal(err)
	}
	return string(data)
}

func (s *suitePublishRig) tip() string {
	s.tt.Helper()
	tip, err := gitx.RevParse(s.clone, "origin/"+s.branch)
	if err != nil {
		s.tt.Fatal(err)
	}
	return tip
}

func (s *suitePublishRig) publish(extra ...string) (string, int) {
	s.tt.Helper()
	return s.run(s.tt, append([]string{"publish", "TK-1", "--yes"}, extra...)...)
}

// TestSuitePublishPassRunsOnceThenOpens: the issue waits on its suite with the
// run named and publish refused with the same code and nothing written; once the
// suite passed at the tip it is ready, publish opens the pull request, and the
// body says the suite.
func TestSuitePublishPassRunsOnceThenOpens(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)

	state, codes, text := s.next()
	if state != "waiting" || len(codes) != 1 || codes[0] != "LAND_SUITE_NOT_RUN" || !strings.Contains(text, "jig verify TK-1 --final") {
		t.Fatalf("jig next TK-1 = %s %v, want waiting on the suite not run\n%s", state, codes, text)
	}
	out, code := s.publish()
	if code == 0 || !strings.Contains(out, "LAND_SUITE_NOT_RUN") || len(publishLines(t, s.chartRig, "TK-1")) != 0 {
		t.Fatalf("publish before the suite ran: exit %d, want LAND_SUITE_NOT_RUN and nothing written\n%s", code, out)
	}

	tip := s.tip()
	s.record(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "pass", Seconds: 7, Command: "go test ./..."})
	if state, codes, _ := s.next(); state != "ready" || len(codes) != 0 {
		t.Fatalf("jig next TK-1 after the pass = %s %v, want ready", state, codes)
	}
	if out, code := s.publish(); code != 0 {
		t.Fatalf("publish: exit %d\n%s", code, out)
	}
	if want := "Suite at the issue's head " + tip[:12] + ": pass in 7 s: `go test ./...`"; !strings.Contains(s.body(), want) || !strings.Contains(s.body(), "## Verification") {
		t.Errorf("the body lacks %q:\n%s", want, s.body())
	}
}

// TestSuitePublishFailAFixTaskAndThePassAgain: a failed suite names the fix
// task and holds publish with LAND_SUITE_FAILED; when the tip moves (a fix task
// landed) the old fail stands for nothing and the suite is asked again, and a
// pass at the new tip releases the issue.
func TestSuitePublishFailAFixTaskAndThePassAgain(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)
	s.record(journal.Line{Event: journal.EventFinalOracle, Commit: s.tip(), Outcome: "fail", Seconds: 9, Signature: "final:boom"})

	state, codes, text := s.next()
	if state != "waiting" || len(codes) != 1 || codes[0] != "LAND_SUITE_FAILED" || !strings.Contains(text, "Add a fix task for TK-1") || !strings.Contains(text, "blocked_by is merged on the chart's last entries") {
		t.Fatalf("jig next TK-1 = %s %v, want the failed suite and the fix-task line\n%s", state, codes, text)
	}
	if out, code := s.publish(); code == 0 || !strings.Contains(out, "LAND_SUITE_FAILED") {
		t.Fatalf("publish after a failed suite: exit %d\n%s", code, out)
	}

	tip := s.moveTip(suiteManifestYAML)
	if _, codes, _ := s.next(); len(codes) != 1 || codes[0] != "LAND_SUITE_NOT_RUN" {
		t.Fatalf("jig next TK-1 at the new tip = %v, want the suite asked again", codes)
	}
	s.record(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "pass", Seconds: 8, Command: "go test ./..."})
	if state, _, _ := s.next(); state != "ready" {
		t.Fatalf("jig next TK-1 after the pass = %s, want ready", state)
	}
}

// TestSuitePublishWaivedIsRecordedAndShown: --without-suite with a reason
// opens the pull request, writes one suite-waived line at the tip with who said
// it, and the body says so; without a reason, or for a task, it is refused.
func TestSuitePublishWaivedIsRecordedAndShown(t *testing.T) {
	s := newSuitePublishRig(t, suiteManifestYAML)

	if out, code := s.publish("--without-suite", ""); code == 0 || !strings.Contains(out, "needs a reason") {
		t.Errorf("an empty reason: exit %d\n%s", code, out)
	}
	if out, code := s.run(t, "publish", "TK-2", "--yes", "--without-suite", "x"); code == 0 || !strings.Contains(out, "only an issue runs a suite") {
		t.Errorf("a task: exit %d\n%s", code, out)
	}
	if lines, _ := journal.Read(s.st, "TK-1"); len(lines) != 0 {
		t.Fatalf("a refused waiver wrote %+v", lines)
	}

	tip := s.tip()
	if out, code := s.publish("--without-suite", "ci runs it"); code != 0 {
		t.Fatalf("publish --without-suite: exit %d\n%s", code, out)
	}
	lines, err := journal.Read(s.st, "TK-1")
	if err != nil {
		t.Fatal(err)
	}
	w, ok := journal.SuiteWaivedAt(lines, "", tip)
	if !ok || w.Summary != "ci runs it" || w.Provenance != journal.ProvenanceAgent {
		t.Errorf("waiver = %+v, %v, want the reason and an agent's word at the tip", w, ok)
	}
	if want := "Suite waived at " + tip[:12] + ": ci runs it"; !strings.Contains(s.body(), want) {
		t.Errorf("the body lacks %q:\n%s", want, s.body())
	}
}

// TestSuitePublishNoSuiteDeclaredIsReady: a repo with no suite holds nothing,
// and the body says so.
func TestSuitePublishNoSuiteDeclaredIsReady(t *testing.T) {
	s := newSuitePublishRig(t, "oracles:\n  test: \"go test\"\n")
	if state, codes, _ := s.next(); state != "ready" || len(codes) != 0 {
		t.Fatalf("jig next TK-1 = %s %v, want ready", state, codes)
	}
	if out, code := s.publish(); code != 0 {
		t.Fatalf("publish: exit %d\n%s", code, out)
	}
	if !strings.Contains(s.body(), "No suite declared in app") {
		t.Errorf("the body lacks the no-suite line:\n%s", s.body())
	}
}
