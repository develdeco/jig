package main

import (
	"flag"
	"io"
	"reflect"
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
