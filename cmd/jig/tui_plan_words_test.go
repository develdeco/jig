package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// landTask records the landing of the rig's task into its issue, merged on the
// host, so the issue's own tail is what stands.
func landTask(t *testing.T, s *suitePublishRig) {
	t.Helper()
	s.green(t, "TK-2")
	landing := journal.Line{Event: journal.EventLanded, Into: "TK-1", Base: s.branch, URL: "https://github.example/owner/repo/pull/2", Commit: "c0ffee2"}
	if err := journal.Append(s.st, "TK-2", landing); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 2, Base: s.branch, State: "closed", Merged: true, MergeCommit: "c0ffee2"}))
}

// planWords prints the plan of TK-1 on a pipe once its status is read, and
// returns its pull-request line and its suite line.
func planWords(t *testing.T, s *suitePublishRig, wantInPR string) (pr, suite string) {
	t.Helper()
	keys, out, wait := startTUI(t, s.env(), "TK-1", "--store", s.root)
	frame := screenWith(t, out, wantInPR)
	// The status is read after the roster and the chart: wait for it, so the line
	// is the one the status gives and not the chart's first guess.
	for i := 0; i < 2000 && strings.Contains(frame, "reading…"); i++ {
		time.Sleep(10 * time.Millisecond)
		frame = lastScreen(out.String())
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui TK-1 exited non-zero\n%s", out.String())
	}
	return frameLineWith(frame, " PR "), frameLineWith(frame, " suite ")
}

// TestThePlanSaysWhatThePersonActsOnAsJigNextDoes: the pull-request words of the
// plan, publish when it is ready, the suite to fix, the tasks to wait for, are
// the container's own standing from `jig status` (its prompt, its waits and its
// unmet) and equal what `jig next` says of the same store, so a break in any hop
// from the verb to the line fails here.
func TestThePlanSaysWhatThePersonActsOnAsJigNextDoes(t *testing.T) {
	// Publish is ready: every task landed and the suite passed at the tip.
	ready := newSuitePublishRig(t, suiteManifestYAML)
	landTask(t, ready)
	ready.record(journal.Line{Event: journal.EventFinalOracle, Commit: ready.tip(), Outcome: "pass", Seconds: 3, Command: "go test ./..."})
	var next struct {
		Issue struct {
			State  string `json:"state"`
			Prompt string `json:"prompt"`
		} `json:"issue"`
	}
	out, code := ready.run(t, "next", "plan", "--json")
	if err := json.Unmarshal([]byte(out), &next); code != 0 || err != nil || next.Issue.State != "ready" || next.Issue.Prompt != "Publish TK-1" {
		t.Fatalf("jig next plan --json: exit %d, %v, the issue is %+v, want ready to publish", code, err, next.Issue)
	}
	status, _ := ready.run(t, "status", "TK-1")
	if !strings.Contains(status, "standing: ready") || !strings.Contains(status, "prompt: Publish TK-1") {
		t.Fatalf("jig status TK-1 does not carry jig next's standing and prompt:\n%s", status)
	}
	if pr, _ := planWords(t, ready, `say "Publish TK-1"`); !strings.Contains(pr, `not opened yet · say "`+next.Issue.Prompt+`"`) {
		t.Errorf("the plan's pull request line is %q, want it to say %q as jig next does", pr, next.Issue.Prompt)
	}

	// A suite that failed: jig next says to add a fix task, and the plan's lines
	// say the suite failed with the same advice and the pull request waits for it.
	failed := newSuitePublishRig(t, suiteManifestYAML)
	landTask(t, failed)
	failed.record(journal.Line{Event: journal.EventFinalOracle, Commit: failed.tip(), Outcome: "fail", Seconds: 3, Signature: "final:boom", Command: "go test ./..."})
	_, codes, text := failed.next()
	if len(codes) != 1 || codes[0] != "LAND_SUITE_FAILED" || !strings.Contains(text, "Add a fix task for TK-1") {
		t.Fatalf("jig next of the failed suite: %v\n%s", codes, text)
	}
	if status, _ := failed.run(t, "status", "TK-1"); !strings.Contains(status, "LAND_SUITE_FAILED") {
		t.Fatalf("jig status TK-1 does not carry the unmet suite:\n%s", status)
	}
	pr, suite := planWords(t, failed, "waits for the suite")
	if !strings.Contains(pr, "not opened yet · waits for the suite") || strings.Contains(pr, "Publish") {
		t.Errorf("the plan's pull request line of a failed suite is %q", pr)
	}
	if !strings.Contains(suite, "failed at") || !strings.Contains(suite, "add a fix task for TK-1") {
		t.Errorf("the plan's suite line of a failed suite is %q", suite)
	}

	// A task not landed: jig next says the issue waits for it.
	waiting := newSuitePublishRig(t, suiteManifestYAML)
	addOpenTask(t, waiting)
	landTask(t, waiting)
	_, _, text = waiting.next()
	if !strings.Contains(text, "TK-3") || !strings.Contains(text, "waits") {
		t.Fatalf("jig next of the issue with a task not landed:\n%s", text)
	}
	if pr, _ := planWords(t, waiting, "waits for 1 task"); !strings.Contains(pr, "not opened yet · waits for 1 task") || strings.Contains(pr, "Publish") {
		t.Errorf("the plan's pull request line with a task not landed is %q", pr)
	}
}
