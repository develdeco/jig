package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/session"
)

// TestStoreRefsSurviveIntoADeltaRound: a round records the store references of
// the whole change, so a line added in round 1 is still noted in round 2,
// which reads only a delta, until the line is gone; and the verdict is clean
// either way.
func TestStoreRefsSurviveIntoADeltaRound(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	commit := func(name, content, msg string) {
		t.Helper()
		build := buildLeaseDir(t, fx)
		if err := os.WriteFile(filepath.Join(build, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(build, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.RunEnv(build, buildGitEnv, "commit", "-m", msg); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(build, "push", "origin", ticketBranch(fx.Ticket)); err != nil {
			t.Fatal(err)
		}
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
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, ReviewResult{ReviewedPaths: req.MustReview, Summary: "clean"}), 0o644)
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

	commit("other.txt", "nothing to note\n", "an unrelated change")
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if reqs[1].Scope != "delta" {
		t.Fatalf("round 2 scope = %q, want delta", reqs[1].Scope)
	}
	if report2.Verdict != "clean" || !reflect.DeepEqual(noted(report2), want) {
		t.Errorf("round 2: verdict %s, store refs %+v; want clean and the round 1 addition %+v", report2.Verdict, report2.StoreRefs, want)
	}

	commit("note.txt", "plain\n", "drop the citation")
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if got := noted(report3); len(got) != 0 {
		t.Errorf("round 3 store refs = %+v, want none once the line is gone", got)
	}
}
