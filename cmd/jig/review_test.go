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

// TestReviewContextShowsTheProbes (ADR 0036): the full view of an open round
// gives each oracle that passed its seconds and lists earlier rounds' probes
// with their state, and counts no hunk and no unprobed one.
func TestReviewContextShowsTheProbes(t *testing.T) {
	p := verifydeliver.Prepared{
		Ticket: "T-1", Round: 2, Branch: "T-1-b", Copy: "the clone's", Head: "bbbb", Base: "aaaa", Scope: "full",
		Request: verifydeliver.ReviewRequest{
			MustReview:    []string{"a/a.go"},
			OraclesPassed: []verifydeliver.SlicePass{{Slice: "s1", Workspace: "a", Command: "go test ./a/...", Commit: "cccccccccccccccccccccccccccccccccccccccc", AtHead: "no", Seconds: 4}},
			Probed: []verifydeliver.ProbedEntry{
				{Round: 1, ResultProbe: verifydeliver.ResultProbe{Seam: "a total", File: "a/a.go", Line: 3, Mutation: "drop the factor", Outcome: verifydeliver.ProbeCaught}, State: verifydeliver.ProbeCurrent},
				{Round: 1, ResultProbe: verifydeliver.ResultProbe{Seam: "a total", File: "a/b.go", Line: 7, Mutation: "negate it", Outcome: verifydeliver.ProbeSurvived}, State: verifydeliver.ProbeStale},
			},
		},
	}
	out := strings.Join(roundBlocks(p, nil, true, nil), "\n")
	for _, want := range []string{
		"oracles_passed[1]{slice,workspace,command,commit,at_head,seconds}:\n  s1,a,go test ./a/...,cccccccccccc,no,4",
		"probed[2]{round,file:line,outcome,state,seam,mutation}:\n  1,\"a/a.go:3\",caught,current,a total,drop the factor\n  1,\"a/b.go:7\",survived,stale,a total,negate it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the round's view lacks %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"hunk", "unprobed"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the round's view says %q\n%s", unwanted, out)
		}
	}
}
