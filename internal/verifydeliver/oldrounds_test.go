package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendTo appends text to the file at path.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, text...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestARoundRecordedWithSkipsAndUnprobedStillLoads: a round an older jig
// recorded, its findings.yaml holding skipped and unprobed keys, loads as it
// did: its findings, probes and coverage fold as before, the gate's next round
// is handed its probe, and a publish renders its probe and the files it read,
// with the old keys ignored.
func TestARoundRecordedWithSkipsAndUnprobedStillLoads(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	useGithubHost(t, &d, "", "")

	var probed ResultProbe
	report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult {
		res := cleanReport(p)
		res.Findings = []ResultFinding{{
			File: p.Request.MustReview[0], Line: 1, Title: "an older round's note", Detail: "d",
			Action: ActionNote, Risk: RiskLow, RiskRationale: "r",
		}}
		probed = ResultProbe{Seam: "an older seam", File: p.Request.MustReview[0], Line: 1, Mutation: "an older mutation", Command: "go test ./alpha", Outcome: ProbeCaught}
		res.Probes = []ResultProbe{probed}
		return res
	})
	if report.Verdict != "clean" || report.Probes != 1 {
		t.Fatalf("round 1 = verdict %q, probes %d, want clean with its probe", report.Verdict, report.Probes)
	}

	// The keys an older jig wrote beside the probes.
	findingsPath := filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "findings.yaml")
	appendTo(t, findingsPath, "skipped:\n"+
		"  - {file: "+probed.File+", why: an older skip, blob: 0000000000000000000000000000000000000000}\n"+
		"unprobed:\n"+
		"  - {file: "+probed.File+", from: 1, to: 2}\n")
	if err := d.Store.Push("an older round"); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadRounds(d.Store, fx.Ticket)
	if err != nil || len(recs) != 1 {
		t.Fatalf("ReadRounds = %d rounds, %v, want 1", len(recs), err)
	}
	r1 := recs[0]
	if r1.Verdict != RoundClean || r1.Files < 1 || len(r1.Findings) != 1 || r1.Findings[0].Title != "an older round's note" ||
		len(r1.Probes) != 1 || r1.Probes[0].Round != 1 || r1.Probes[0].Mutation != "an older mutation" {
		t.Fatalf("round 1 read back = %+v, want its verdict, files, finding and probe", r1)
	}
	fold, err := FoldBefore(d.Store, fx.Ticket, 2)
	if err != nil || len(fold.Open) != 1 || fold.Open[0].ID != "r1-f1" || len(fold.Probes) != 1 {
		t.Fatalf("FoldBefore = %+v, %v, want the note r1-f1 and the probe", fold, err)
	}

	// The next round, over a change that leaves the probed file alone.
	build := buildLeaseDir(t, fx)
	if err := os.WriteFile(filepath.Join(build, "after-the-older-round.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder(t).Commit(build, "change after the older round")
	builder(t).Push(build, "origin", ticketBranch(fx.Ticket))
	var req2 ReviewRequest
	report2 := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult {
		req2 = p.Request
		return cleanReport(p)
	})
	if len(req2.Probed) != 1 || req2.Probed[0].ResultProbe != probed || req2.Probed[0].State != ProbeCurrent {
		t.Errorf("round 2 probed = %+v, want round 1's probe, current", req2.Probed)
	}
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 verdict = %q, want clean", report2.Verdict)
	}

	pub, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(pub.Repos[0].ReviewNotes)))
	if err != nil {
		t.Fatal(err)
	}
	notes := string(data)
	for _, want := range []string{
		"an older round's note",
		"Reviewed paths:\n\n",
		"- " + probed.File + "\n",
		"Probes current at the reviewed head:\n\n- " + probed.File + ":1 caught: an older mutation (an older seam; round 1)",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the review notes do not hold %q:\n%s", want, notes)
		}
	}
	for _, unwanted := range []string{"an older skip", "Set aside", "no probe or skip", "unprobed"} {
		if strings.Contains(notes, unwanted) {
			t.Errorf("the review notes hold %q:\n%s", unwanted, notes)
		}
	}
}

// TestAnOpenRoundWithASkipDraftStillCloses: a round an older jig left open,
// its review.json listing hunks and its drafts holding a skip, closes: the skip
// is ignored, the drafted probe is kept, and findings.yaml gets no skipped or
// unprobed key.
func TestAnOpenRoundWithASkipDraftStillCloses(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)

	opened, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	file := opened.Request.MustReview[0]
	probe := ResultProbe{Seam: "s", File: file, Line: 1, Mutation: "m", Command: "go test ./alpha", Outcome: ProbeCaught}

	// What the older jig left: hunks in the open round's request, and a skip
	// among its drafts.
	requestPath := reviewJSONPath(d.Store, fx.Ticket, opened.Round)
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	request["hunks"] = []any{map[string]any{"file": file, "from": 1, "to": 2, "probes": 0}}
	if data, err = json.Marshal(request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(opened.DraftsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opened.DraftsPath, []byte(`{"skip":{"file":"`+file+`","from":0,"to":0,"why":"tests"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, open, err := CurrentRound(d, fx.Ticket)
	if err != nil || !open {
		t.Fatalf("CurrentRound = open %v, %v, want the round open", open, err)
	}
	if _, err := AppendDraft(d, p, Draft{Probe: &probe}); err != nil {
		t.Fatalf("AppendDraft of a probe beside the skip: %v", err)
	}
	drafts, err := ReadDrafts(p)
	if err != nil || len(drafts) != 1 || drafts[0].Probe == nil {
		t.Fatalf("drafts = %+v, %v, want the probe alone", drafts, err)
	}
	report, err := GateFinish(d, p, DraftResult(drafts, p.Request.MustReview, "closed"), Provenance{Session: "s-reviewer", How: ReviewerByFlag})
	if err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	if report.Verdict != "clean" || report.Probes != 1 {
		t.Errorf("report = verdict %q, probes %d, want clean with the drafted probe", report.Verdict, report.Probes)
	}
	written, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, p.Round), "findings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, p.Round)
	if err != nil || !ok || len(ff.Probes) != 1 || ff.Probes[0].result() != probe {
		t.Errorf("findings.yaml probes = %+v (ok=%v, %v), want the drafted probe", ff.Probes, ok, err)
	}
	for _, key := range []string{"skipped:", "unprobed:"} {
		if strings.Contains(string(written), key) {
			t.Errorf("findings.yaml holds %q:\n%s", key, written)
		}
	}
}
