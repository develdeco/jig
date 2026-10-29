package main

import (
	"flag"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestReviewerFromFlagsPrefersTheFlagThenTheEnvironment: the reviewer is the
// session the verb names, else $JIG_SESSION, else the id the harness set,
// else nobody - and how jig knows is recorded with it, because a round trusts
// a flag more than a guess.
func TestReviewerFromFlagsPrefersTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()
	withEnv := envFrom(map[string]string{"JIG_SESSION": "s-from-env", "CLAUDE_CODE_SESSION_ID": "s-from-claude"})
	harness := envFrom(map[string]string{"CLAUDE_CODE_SESSION_ID": "s-from-claude"})
	none := envFrom(nil)
	cases := []struct {
		name    string
		e       env
		session string
		want    verifydeliver.Provenance
	}{
		{"flag over env", withEnv, "s-flag", verifydeliver.Provenance{Session: "s-flag", How: verifydeliver.ReviewerByFlag}},
		{"env alone", withEnv, "", verifydeliver.Provenance{Session: "s-from-env", How: verifydeliver.ReviewerByEnv}},
		{"harness alone", harness, "", verifydeliver.Provenance{Session: "s-from-claude", How: verifydeliver.ReviewerByEnv}},
		{"nobody", none, "", verifydeliver.Provenance{}},
	}
	for _, c := range cases {
		if got := reviewerFromFlags(c.e, c.session); got != c.want {
			t.Errorf("%s: reviewerFromFlags = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestStringListSplitsOnCommasAndCollectsRepeats: `--reviewed a,b --reviewed
// c` is the three paths, with blanks and surrounding spaces dropped.
func TestStringListSplitsOnCommasAndCollectsRepeats(t *testing.T) {
	t.Parallel()
	var l stringList
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&l, "reviewed", "")
	if err := fs.Parse([]string{"--reviewed", "alpha/a.go, beta/b.go,", "--reviewed", "gamma/c.go", "--reviewed", " "}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha/a.go", "beta/b.go", "gamma/c.go"}; !reflect.DeepEqual([]string(l), want) {
		t.Errorf("--reviewed = %q, want %q", []string(l), want)
	}
	if got := l.String(); got != "alpha/a.go,beta/b.go,gamma/c.go" {
		t.Errorf("String() = %q", got)
	}
}

// TestReviewContextShowsTheProbeLedger (ADR 0036): the full view of an open
// round counts the change's hunks and the unprobed ones, gives each oracle
// that passed its seconds and slices, lists earlier rounds' probes with their
// state, and lists the unprobed hunks as the sweep's work list.
func TestReviewContextShowsTheProbeLedger(t *testing.T) {
	p := verifydeliver.Prepared{
		Ticket: "T-1", Round: 2, Branch: "T-1-b", Copy: "the clone's", Head: "bbbb", Base: "aaaa", Scope: "delta", Lease: "/lease",
		Request: verifydeliver.ReviewRequest{
			MustReview:    []string{"a/a.go"},
			OraclesPassed: []verifydeliver.OracleRun{{Oracle: "test", Workspace: "a", Command: "go test ./a/...", Seconds: 4, Slices: []string{"s1", "s2"}}},
			Probed: []verifydeliver.ProbedEntry{
				{Round: 1, ResultProbe: verifydeliver.ResultProbe{Seam: "a total", File: "a/a.go", Line: 3, Mutation: "drop the factor", Outcome: verifydeliver.ProbeCaught}, State: verifydeliver.ProbeCurrent},
				{Round: 1, ResultProbe: verifydeliver.ResultProbe{Seam: "a total", File: "a/b.go", Line: 7, Mutation: "negate it", Outcome: verifydeliver.ProbeSurvived}, State: verifydeliver.ProbeStale},
			},
			Hunks: []verifydeliver.HunkEntry{
				{File: "a/a.go", From: 2, To: 4, Probes: 1},
				{File: "a/a_test.go", From: 1, To: 9, Skip: "tests"},
				{File: "a/b.go", From: 6, To: 8},
			},
		},
	}
	out := strings.Join(roundBlocks(p, nil, true), "\n")
	for _, want := range []string{
		"hunks: 3",
		"unprobed: 1",
		"oracles_passed[1]{oracle,workspace,seconds,slices,reused_from}:\n  test,a,4,\"s1,s2\",",
		"probed[2]{round,file:line,outcome,state,seam,mutation}:\n  1,\"a/a.go:3\",caught,current,a total,drop the factor\n  1,\"a/b.go:7\",survived,stale,a total,negate it",
		"unprobed[1]{hunk}:\n  \"a/b.go:6-8\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the round's view lacks %q\n%s", want, out)
		}
	}
	if brief := strings.Join(roundBlocks(p, nil, false), "\n"); strings.Contains(brief, "unprobed") {
		t.Errorf("the short view of a round counts unprobed hunks, want only the full one to\n%s", brief)
	}
}
