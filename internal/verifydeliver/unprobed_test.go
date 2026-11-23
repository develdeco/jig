package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRoundKeepsItsUnprobedHunks: a round a reviewer ran writes the hunks it
// left unprobed into findings.yaml, the list whose length the report counts; a
// round that left none writes an empty list, and a findings.yaml an older jig
// wrote reads as not recorded.
func TestRoundKeepsItsUnprobedHunks(t *testing.T) {
	t.Parallel()

	t.Run("a gate round writes the hunks it left", func(t *testing.T) {
		t.Parallel()
		fx := generateBuilt(t, "rung-a")
		d := newDeps(t, fx)
		var req ReviewRequest
		report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult {
			req = p.Request
			h := p.Request.Hunks[0]
			return ReviewResult{ReviewedPaths: p.Request.MustReview, Summary: "s", Probes: []ResultProbe{
				{Seam: "s", File: h.File, Line: h.From, Mutation: "m", Command: "c", Outcome: ProbeCaught},
			}}
		})
		ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
		if err != nil || !ok || ff.Unprobed == nil {
			t.Fatalf("round 1 findings.yaml: ok=%v err=%v unprobed=%v, want a recorded list", ok, err, ff.Unprobed)
		}
		got := *ff.Unprobed
		if len(got) != report.Unprobed || len(got) != len(req.Hunks)-1 {
			t.Fatalf("recorded %d hunks, report counts %d, %d hunks less the probed one", len(got), report.Unprobed, len(req.Hunks))
		}
		for i, h := range req.Hunks[1:] {
			if got[i] != (UnprobedHunk{File: h.File, From: h.From, To: h.To}) {
				t.Errorf("unprobed[%d] = %+v, want hunk %+v", i, got[i], h)
			}
		}
	})

	t.Run("a gate round that covered every hunk writes an empty list", func(t *testing.T) {
		t.Parallel()
		fx := generateBuilt(t, "rung-a")
		d := newDeps(t, fx)
		report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult {
			res := ReviewResult{ReviewedPaths: p.Request.MustReview, Summary: "s"}
			seen := map[string]bool{}
			for _, h := range p.Request.Hunks {
				if !seen[h.File] {
					seen[h.File] = true
					res.Skipped = append(res.Skipped, ResultSkip{File: h.File, Why: "set aside"})
				}
			}
			return res
		})
		ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
		if err != nil || !ok {
			t.Fatalf("round 1 findings.yaml: ok=%v err=%v", ok, err)
		}
		if report.Unprobed != 0 || ff.Unprobed == nil || len(*ff.Unprobed) != 0 {
			t.Errorf("a round that left none: report counts %d, findings.yaml unprobed = %v, want 0 and a recorded empty list", report.Unprobed, ff.Unprobed)
		}
	})

	t.Run("a round that left none writes an empty list", func(t *testing.T) {
		t.Parallel()
		data, err := marshalFindingsYAML("full", nil, nil, nil, nil, "", nil, nil, []UnprobedHunk{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "unprobed: []") {
			t.Errorf("findings.yaml has no explicit empty list:\n%s", data)
		}
		var ff findingsYAML
		if err := yaml.Unmarshal(data, &ff); err != nil || ff.Unprobed == nil || len(*ff.Unprobed) != 0 {
			t.Errorf("read back unprobed = %v, err %v, want recorded and empty", ff.Unprobed, err)
		}
	})

	t.Run("a multi-repo file keeps the name the round gave it", func(t *testing.T) {
		t.Parallel()
		want := []UnprobedHunk{{File: "api/internal/x.go", From: 3, To: 9}, {File: "web/y.ts", From: 1, To: 2}}
		data, err := marshalFindingsYAML("full", nil, nil, nil, nil, "", nil, nil, want)
		if err != nil {
			t.Fatal(err)
		}
		var ff findingsYAML
		if err := yaml.Unmarshal(data, &ff); err != nil || ff.Unprobed == nil || !reflect.DeepEqual(*ff.Unprobed, want) {
			t.Errorf("read back %v, err %v, want %v", ff.Unprobed, err, want)
		}
	})

	t.Run("an older findings.yaml reads as not recorded", func(t *testing.T) {
		t.Parallel()
		data, err := marshalFindingsYAML("full", nil, nil, nil, nil, "", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "unprobed") {
			t.Errorf("a round that did not record the list writes the key:\n%s", data)
		}
		var ff findingsYAML
		if err := yaml.Unmarshal([]byte("scope: full\nreviewed_paths: []\nfindings: []\n"), &ff); err != nil || ff.Unprobed != nil {
			t.Errorf("older file reads unprobed = %v, err %v, want nil", ff.Unprobed, err)
		}
	})
}

// TestReadRoundsCarriesTheLedger: ReadRounds hands back each round's probes
// and skips with their round, and its unprobed hunks with whether the round
// recorded them.
func TestReadRoundsCarriesTheLedger(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	const ticket = "T-1"
	write := func(n, body string) {
		dir := filepath.Join(st.TicketDir(ticket), "gate", "round-"+n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte("round: "+n+"\nverdict: clean\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("1", "scope: full\nreviewed_paths: []\nfindings: []\nprobes:\n"+
		"  - {seam: s1, file: a.go, line: 4, mutation: m1, command: c1, outcome: survived, blob: b1}\n"+
		"  - {seam: s2, file: a.go, line: 9, mutation: m2, command: c2, outcome: caught, blob: b1}\n"+
		"skipped:\n  - {file: t_test.go, why: tests, blob: b2}\n"+
		"unprobed:\n  - {file: c.go, from: 1, to: 5}\n")
	write("2", "scope: delta\nreviewed_paths: []\nfindings: []\nunprobed: []\n")
	write("3", "scope: delta\nreviewed_paths: []\nfindings: []\n")
	write("4", "")

	recs, err := ReadRounds(st, ticket)
	if err != nil || len(recs) != 4 {
		t.Fatalf("ReadRounds = %d rounds, %v, want 4", len(recs), err)
	}
	r1 := recs[0]
	if len(r1.Probes) != 2 || r1.Probes[0].Round != 1 || r1.Probes[0].Outcome != ProbeSurvived || r1.Probes[1].Line != 9 {
		t.Errorf("round 1 probes = %+v", r1.Probes)
	}
	if len(r1.Skipped) != 1 || r1.Skipped[0].Round != 1 || r1.Skipped[0].Why != "tests" {
		t.Errorf("round 1 skipped = %+v", r1.Skipped)
	}
	if !r1.UnprobedRecorded || !reflect.DeepEqual(r1.Unprobed, []UnprobedHunk{{File: "c.go", From: 1, To: 5}}) {
		t.Errorf("round 1 unprobed = %+v recorded=%v", r1.Unprobed, r1.UnprobedRecorded)
	}
	if !recs[1].UnprobedRecorded || len(recs[1].Unprobed) != 0 || len(recs[1].Probes) != 0 {
		t.Errorf("round 2 = %+v, want recorded, none left, no probes", recs[1])
	}
	for _, i := range []int{2, 3} {
		if recs[i].UnprobedRecorded || recs[i].Unprobed != nil || recs[i].Probes != nil {
			t.Errorf("round %d = %+v, want nothing recorded", i+1, recs[i])
		}
	}
}
