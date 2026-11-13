package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/session"
)

// TestStoreRefsSurviveIntoADeltaRound: a round records the store references of
// the whole change, so a line added in round 1 is still noted in round 2,
// which reads only a delta, until the line is gone; and the verdict is clean
// either way.
func TestStoreRefsSurviveIntoADeltaRound(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)

	commit := func(name, content, msg string) {
		t.Helper()
		build := buildLeaseDir(t, fx)
		if err := os.WriteFile(filepath.Join(build, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		builder(t).Commit(build, msg)
		builder(t).Push(build, "origin", ticketBranch(fx.Ticket))
	}
	var reqs []ReviewRequest
	src := newReviewerGateSource(stubBackend{run: func(sd session.Dispatch) error {
		data, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		reqs = append(reqs, req)
		result := ReviewResult{ReviewedPaths: req.MustReview, Summary: "clean"}
		if len(reqs) == 1 {
			// Round 1 reports a finding, whose id a later line can cite.
			result.Findings = []ResultFinding{{
				File: "note.txt", Line: 1, Title: "an aside", Detail: "nothing to fix", Action: ActionNote,
				Risk: RiskLow, RiskRationale: "an aside",
			}}
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}})
	noted := func(report GateReport) []StoreRef {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, report.Round), "report.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rep.StoreRefs, report.StoreRefs) {
			t.Errorf("report.yaml store_refs = %+v, the report has %+v", rep.StoreRefs, report.StoreRefs)
		}
		return rep.StoreRefs
	}

	commit("note.txt", "plain\n// see "+fx.Ticket+" for why\n", "cite the ticket")
	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.StoreRefs) == 0 {
		t.Fatal("round 1 noted no store reference")
	}
	want := []StoreRef{{Repo: report1.StoreRefs[0].Repo, File: "note.txt", Line: 2, ID: fx.Ticket}}
	if report1.Verdict != "clean" || !reflect.DeepEqual(noted(report1), want) || !reflect.DeepEqual(reqs[0].StoreRefs, want) {
		t.Fatalf("round 1: verdict %s, store refs %+v, request %+v; want clean and %+v", report1.Verdict, report1.StoreRefs, reqs[0].StoreRefs, want)
	}

	commit("other.txt", "cites r1-f1 here\n", "cite the finding of round 1")
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if reqs[1].Scope != "delta" {
		t.Fatalf("round 2 scope = %q, want delta", reqs[1].Scope)
	}
	want2 := append(append([]StoreRef(nil), want...), StoreRef{Repo: want[0].Repo, File: "other.txt", Line: 1, ID: "r1-f1"})
	sort.Slice(want2, func(i, j int) bool { return want2[i].File < want2[j].File })
	if report2.Verdict != "clean" || !reflect.DeepEqual(noted(report2), want2) {
		t.Errorf("round 2: verdict %s, store refs %+v; want clean, the round 1 addition and the citation of round 1's finding id: %+v", report2.Verdict, report2.StoreRefs, want2)
	}

	commit("note.txt", "plain\n", "drop the citation")
	commit("other.txt", "nothing\n", "drop the second citation")
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if got := noted(report3); len(got) != 0 {
		t.Errorf("round 3 store refs = %+v, want none once the line is gone", got)
	}
}
