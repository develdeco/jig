package verifydeliver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRecordedRoundKeepsTheMachinesPathsOutOfTheStore: a finding and a summary
// that cite a recording, the lease or the store by their absolute paths, in any
// spelling a path has, reach the store with the jig home, the lease and the
// store left out of every free-text field, in findings.yaml and in every file
// the round committed; an absolute reviewed path inside the lease still counts
// as coverage.
func TestRecordedRoundKeepsTheMachinesPathsOutOfTheStore(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	rec := filepath.Join(d.Home, "evidence", "abc", "recording.svg")

	var lease string
	report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket, Yes: true}, func(p Prepared) ReviewResult {
		lease = p.Lease
		var abs []string
		for _, f := range p.Request.MustReview {
			abs = append(abs, filepath.Join(p.Lease, filepath.FromSlash(f)))
		}
		return ReviewResult{
			Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Action: ActionNote, Risk: RiskLow, Oracle: "test",
				Title:         "the screen in " + rec + " is wrong",
				Detail:        "see " + filepath.ToSlash(rec) + " and " + fmt.Sprintf("%q", rec) + ", checked in " + p.Lease,
				RiskRationale: "from " + wslMountPath(rec),
			}},
			ReviewedPaths: abs,
			Summary:       "read " + d.Home + " and " + d.Store.Root,
		}
	})
	if report.Verdict != "clean" {
		t.Fatalf("verdict = %q, want clean: an absolute reviewed path in the lease covers its file", report.Verdict)
	}
	ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok || len(ff.Findings) != 1 {
		t.Fatalf("findings.yaml = %+v, ok %v, err %v", ff.Findings, ok, err)
	}
	f := ff.Findings[0]
	if !strings.Contains(f.Title, "<jig home>") || !strings.Contains(f.Detail, "<lease>") || !strings.Contains(f.RiskRationale, "<jig home>") {
		t.Errorf("the paths were not replaced by their names: title %q, detail %q, rationale %q", f.Title, f.Detail, f.RiskRationale)
	}
	dirs := []string{d.Home, lease, d.Store.Root}
	err = filepath.WalkDir(d.Store.Root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if de.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// What the reviewer's words reach: the round's files and the result.json
		// kept beside the request (the request itself is jig's, not theirs).
		slash := filepath.ToSlash(path)
		if !strings.Contains(slash, "/gate/round-1/") && !strings.HasSuffix(slash, "result.json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			if found := hostSpellingsIn(string(data), dir); len(found) > 0 {
				t.Errorf("%s holds %v", path, found)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRecordedRoundKeepsTheStoreRefsOfTheWholeChange: a round records the store
// references of the whole change, so a line added before round 1 is still noted
// in round 2, which reads only a delta, until the line is gone; and the verdict
// is clean either way.
func TestRecordedRoundKeepsTheStoreRefsOfTheWholeChange(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	o := GateOpts{Ticket: fx.Ticket, Yes: true}

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
	reviewer := func(p Prepared) ReviewResult {
		reqs = append(reqs, p.Request)
		res := cleanReport(p)
		if len(reqs) == 1 {
			// Round 1 reports a finding, whose id a later line can cite.
			res.Findings = []ResultFinding{{
				File: "note.txt", Line: 1, Title: "an aside", Detail: "nothing to fix", Action: ActionNote,
				Risk: RiskLow, RiskRationale: "an aside",
			}}
		}
		return res
	}
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
	report1 := recordedRound(t, d, o, reviewer)
	if len(report1.StoreRefs) == 0 {
		t.Fatal("round 1 noted no store reference")
	}
	want := []StoreRef{{Repo: report1.StoreRefs[0].Repo, File: "note.txt", Line: 2, ID: fx.Ticket}}
	if report1.Verdict != "clean" || !reflect.DeepEqual(noted(report1), want) || !reflect.DeepEqual(reqs[0].StoreRefs, want) {
		t.Fatalf("round 1: verdict %s, store refs %+v, request %+v; want clean and %+v", report1.Verdict, report1.StoreRefs, reqs[0].StoreRefs, want)
	}

	commit("other.txt", "cites r1-f1 here\n", "cite the finding of round 1")
	report2 := recordedRound(t, d, o, reviewer)
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
	report3 := recordedRound(t, d, o, reviewer)
	if got := noted(report3); len(got) != 0 {
		t.Errorf("round 3 store refs = %+v, want none once the line is gone", got)
	}
}
