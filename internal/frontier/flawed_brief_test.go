package frontier

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestFlawedBriefWithoutABriefIsAPlainQuestion: a builder reporting a flawed
// brief on a ticket that has no brief.md - one that adopted a branch, or was
// never given a brief - finds nothing to amend and nothing for `jig requeue
// --from-brief-diff` to diff. The slice parks with the session's finding as a
// plain question, resumed by answering it like any other: no "flawed-brief"
// reason on the slice, and no instruction to amend a brief in the question.
// The journal still says what the builder reported.
func TestFlawedBriefWithoutABriefIsAPlainQuestion(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "flawed-brief"})
	d, st := newDeps(t, fx)
	if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.NeedsInput) != 1 || report.NeedsInput[0] != "c" || report.PendingQuestion == "" {
		t.Fatalf("report = %+v, want slice c parked on a question", report)
	}
	cs, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cs.State != "needs-input" || cs.Reason != "" {
		t.Fatalf("slice c state/reason = %q/%q, want needs-input with no flawed-brief reason: there is no brief to amend", cs.State, cs.Reason)
	}

	raw, err := os.ReadFile(filepath.Join(fx.ScenarioDir, "slices", "c", "attempt-1", "result.json"))
	if err != nil {
		t.Fatalf("read the scenario's result.json: %v", err)
	}
	var scripted struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &scripted); err != nil {
		t.Fatalf("parse the scenario's result.json: %v", err)
	}
	questions, err := st.ReadQuestions(fx.Ticket)
	if err != nil || len(questions) != 1 {
		t.Fatalf("questions = %+v (err %v), want one", questions, err)
	}
	q := questions[0]
	if strings.TrimSpace(q.Body) != strings.TrimSpace(scripted.Summary) {
		t.Fatalf("question body = %q, want the session's own finding %q", q.Body, scripted.Summary)
	}
	for _, banned := range []string{"requeue", "--from-brief-diff", "amend"} {
		if strings.Contains(q.Body, banned) {
			t.Fatalf("question body %q tells the human to %q, which needs a brief this ticket does not have", q.Body, banned)
		}
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var reported bool
	for _, l := range lines {
		if l.Event == "question" && l.Slice == "c" && l.Outcome == "flawed-brief" {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the journal has no question line for slice c reporting flawed-brief: %+v", lines)
	}
}

// TestFlawedBriefWithABriefStillPointsAtTheBrief is the other side: with a
// brief.md there is something to amend, so the parked slice keeps its
// flawed-brief reason (status offers the requeue path) and the question still
// tells the human to amend the brief and requeue.
func TestFlawedBriefWithABriefStillPointsAtTheBrief(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "flawed-brief"})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cs, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cs.State != "needs-input" || cs.Reason != "flawed-brief" {
		t.Fatalf("slice c state/reason = %q/%q, want needs-input/flawed-brief", cs.State, cs.Reason)
	}
	questions, err := st.ReadQuestions(fx.Ticket)
	if err != nil || len(questions) != 1 {
		t.Fatalf("questions = %+v (err %v), want one", questions, err)
	}
	for _, want := range []string{"amend brief.md section(s)", "jig requeue " + fx.Ticket + " --from-brief-diff"} {
		if !strings.Contains(questions[0].Body, want) {
			t.Fatalf("question body %q does not say %q", questions[0].Body, want)
		}
	}
}

// TestFlawedBriefOnASliceWithNoBriefSectionsIsAPlainQuestion: whether amending
// the brief is the remedy is decided by the slice, not by whether the ticket
// has a brief: a slice that cites no brief section - a gate fix slice is one -
// has nothing to amend and nothing for `jig requeue --from-brief-diff` to
// notice, so on a ticket that does have a brief it is still a plain question,
// answered like any other. This is the rule `jig status` resumes a parked
// slice by, so the question and the status hint cannot disagree.
func TestFlawedBriefOnASliceWithNoBriefSectionsIsAPlainQuestion(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "flawed-brief"})
	d, st := newDeps(t, fx)
	if _, err := os.Stat(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("test setup: the ticket must have a brief: %v", err)
	}
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for i := range slices {
		if slices[i].ID == "c" {
			slices[i].FromBrief = nil
		}
	}
	data, err := yaml.Marshal(store.SliceFile{Slices: slices})
	if err != nil {
		t.Fatalf("marshal slices: %v", err)
	}
	if err := os.WriteFile(filepath.Join(st.TicketDir(fx.Ticket), "slices.yaml"), data, 0o644); err != nil {
		t.Fatalf("write slices.yaml: %v", err)
	}

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cs, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cs.State != "needs-input" || cs.Reason != "" {
		t.Fatalf("slice c state/reason = %q/%q, want needs-input with no flawed-brief reason: it cites no brief section to amend", cs.State, cs.Reason)
	}
	questions, err := st.ReadQuestions(fx.Ticket)
	if err != nil || len(questions) != 1 {
		t.Fatalf("questions = %+v (err %v), want one", questions, err)
	}
	for _, banned := range []string{"requeue", "--from-brief-diff", "amend"} {
		if strings.Contains(questions[0].Body, banned) {
			t.Fatalf("question body %q tells the human to %q, which needs brief sections this slice does not cite", questions[0].Body, banned)
		}
	}
}

// TestRequeueFromBriefDiffWithoutABriefIsRefused: `jig requeue
// --from-brief-diff` compares a brief's sections with the slices', so a ticket
// with no brief.md - an adopted one is - is refused with an axi code and the
// slices' own remedies, not a raw "no such file" failure. A ticket that has a
// brief is unaffected.
func TestRequeueFromBriefDiffWithoutABriefIsRefused(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	if _, err := Requeue(d, fx.Ticket, true); err != nil {
		t.Fatalf("Requeue on a ticket with a brief: %v", err)
	}

	if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}
	_, err := Requeue(d, fx.Ticket, true)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("Requeue with no brief: err = %v, want an *axi.Error VALIDATION_ERROR", err)
	}
	help := strings.Join(ae.Help, "\n")
	if !strings.Contains(ae.Msg, "no brief.md") || !strings.Contains(help, "--slice") || !strings.Contains(help, "--answer") {
		t.Fatalf("the refusal = %q / %q, want it to name the missing brief and the slices' own remedies", ae.Msg, help)
	}
}
