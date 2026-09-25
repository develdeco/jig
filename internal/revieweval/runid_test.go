package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/session"
)

// --- every case gets an opaque run id ----------------------------------

// capturedRunIDDispatch is one dispatch capturingRunIDBackend recorded:
// everything the reviewer or judge must never see the case name in - the rendered prompt, the paths jig wrote it, and (read back at
// dispatch time, before this round's own work dir is deleted) the
// actual file content at SliceJSON.
type capturedRunIDDispatch struct {
	Slice, Ticket, Prompt, SliceJSON, ResultJSON, SliceJSONContent string
}

// capturingRunIDBackend records every dispatch it is given and answers a
// "gate" dispatch with nil-deref's own perfect fixture result (so the
// round actually passes end to end) and a "judge" dispatch with a fixed
// Same verdict for every candidate - enough to drive both a reviewer and a
// judge dispatch through one real RunCase.
type capturingRunIDBackend struct {
	dispatches *[]capturedRunIDDispatch
}

const runIDTestGateResult = `{
  "findings": [
    {
      "file": "users/lookup.go",
      "line": 17,
      "title": "Lookup dereferences a nil map entry",
      "detail": "Lookup looks up id in store and calls u.Name without checking whether the map returned nil, so an unknown id panics on the nil pointer.",
      "action": "fix",
      "risk": "medium",
      "risk_rationale": "An unknown id crashes the caller instead of returning an empty string."
    }
  ],
  "reviewed_paths": ["users/lookup.go"],
  "summary": "Lookup panics on an unknown id."
}`

func (b capturingRunIDBackend) Run(d session.Dispatch) error {
	content, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		return fmt.Errorf("read %s: %w", d.SliceJSON, err)
	}
	*b.dispatches = append(*b.dispatches, capturedRunIDDispatch{
		Slice: d.Slice, Ticket: d.Ticket, Prompt: d.Prompt,
		SliceJSON: d.SliceJSON, ResultJSON: d.ResultJSON, SliceJSONContent: string(content),
	})

	switch d.Slice {
	case "gate":
		return os.WriteFile(d.ResultJSON, []byte(runIDTestGateResult), 0o644)
	case "judge":
		// runIDTestGateResult's one finding sits exactly on nil-deref's
		// one gold span, so MatchRound builds exactly one candidate:
		// answering candidate 0 Same is enough for this round to pass.
		return os.WriteFile(d.ResultJSON, []byte(`{"verdicts":[{"candidate":0,"same":true}]}`), 0o644)
	default:
		return fmt.Errorf("capturingRunIDBackend: unexpected slice %q", d.Slice)
	}
}

// TestRunCaseNeverNamesTheCaseToTheReviewerOrJudge drives a
// prompt-and-input-capturing backend and judge through RunCase, on a case
// whose name ("nil-deref") is exactly the kind of clue the run id exists
// to hide. Nothing any dispatch carries or reads - the rendered prompt, the
// review.json/judge.json path, or that file's own content - may contain
// the case name, and no commit message in the case repo may either.
func TestRunCaseNeverNamesTheCaseToTheReviewerOrJudge(t *testing.T) {
	c := loadEvalCase(t, "nil-deref")
	var dispatches []capturedRunIDDispatch
	backend := capturingRunIDBackend{dispatches: &dispatches}
	judge := &ModelJudge{Backend: backend, Model: "fixture-model"}

	workDir := t.TempDir()
	cs, err := RunCase(workDir, c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if len(cs.Rounds) != 1 || !cs.Rounds[0].Passed {
		t.Fatalf("RunCase: want one passed round (the judge confirmed the gold match), got %+v", cs.Rounds)
	}

	if len(dispatches) != 2 {
		t.Fatalf("dispatches = %d, want 2 (one gate, one judge)", len(dispatches))
	}
	for _, d := range dispatches {
		if strings.Contains(d.Ticket, c.Name) {
			t.Errorf("%s dispatch Ticket = %q, contains the case name", d.Slice, d.Ticket)
		}
		if strings.Contains(d.Prompt, c.Name) {
			t.Errorf("%s dispatch Prompt contains the case name:\n%s", d.Slice, d.Prompt)
		}
		if strings.Contains(d.SliceJSON, c.Name) {
			t.Errorf("%s dispatch SliceJSON path %q contains the case name", d.Slice, d.SliceJSON)
		}
		if strings.Contains(d.ResultJSON, c.Name) {
			t.Errorf("%s dispatch ResultJSON path %q contains the case name", d.Slice, d.ResultJSON)
		}
		if strings.Contains(d.SliceJSONContent, c.Name) {
			t.Errorf("%s dispatch's own %s content contains the case name:\n%s", d.Slice, d.SliceJSON, d.SliceJSONContent)
		}
	}

	repoDir := filepath.Join(workDir, "repo")
	log, err := gitx.Run(repoDir, "log", "--format=%s")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if strings.Contains(log, c.Name) {
		t.Errorf("case repo commit messages contain the case name:\n%s", log)
	}
}
