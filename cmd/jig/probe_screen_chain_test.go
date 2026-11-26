package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
	"gopkg.in/yaml.v3"
)

// TestReviewScreenShowsARecordedRoundsProbes follows a round's probes through
// the real verbs: a reviewer records a survivor and a caught probe with jig
// probe and closes the round with jig review done; findings.yaml holds them;
// jig status prints them; and the review screen of jig tui, on a pipe, shows the
// round's counts and the survivor's row. A second round that probes the
// survivor's line again and catches it marks the first row as superseded.
// Neither the status nor the screen shows a skip or an unprobed hunk.
func TestReviewScreenShowsARecordedRoundsProbes(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	flags := []string{"--store", fx.StoreDir}
	withFlags := func(args ...string) []string { return append(args, flags...) }
	must := func(args ...string) string {
		t.Helper()
		out, code := runMain(t, e, "", withFlags(args...)...)
		if code != 0 {
			t.Fatalf("jig %s: exit = %d\n%s", strings.Join(args, " "), code, out)
		}
		return out
	}
	contextOf := func() verifydeliver.ReviewRequest {
		t.Helper()
		var req verifydeliver.ReviewRequest
		if err := json.Unmarshal([]byte(must("review", "context", ticket, "--json")), &req); err != nil {
			t.Fatalf("review context --json: %v", err)
		}
		return req
	}
	probe := func(file string, line int, mutation, outcome string) {
		t.Helper()
		must("probe", ticket, "--seam", "the bound of a percentage", "--file", file, "--line", strconv.Itoa(line),
			"--mutation", mutation, "--command", "go test ./alpha", "--outcome", outcome)
	}

	must("gate", ticket)
	req := contextOf()
	if len(req.MustReview) < 2 {
		t.Fatalf("must_review = %v, want at least two files", req.MustReview)
	}
	survivor, caught := req.MustReview[0], req.MustReview[1]
	probe(survivor, 1, "raise the bound", verifydeliver.ProbeSurvived)
	probe(caught, 1, "drop the guard", verifydeliver.ProbeCaught)
	must("review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "probed")

	var ff struct {
		Probes []struct{ File, Outcome string }
	}
	data, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1", "findings.yaml"))
	if err != nil || yaml.Unmarshal(data, &ff) != nil {
		t.Fatalf("read findings.yaml: %v\n%s", err, data)
	}
	if len(ff.Probes) != 2 {
		t.Fatalf("findings.yaml probes %+v, want two", ff.Probes)
	}

	status := must("status", ticket)
	for _, want := range []string{
		"probes[2]{round,outcome,file:line,seam,mutation,command}:",
		",probes,survived}:",
		",2,1\n", // the rounds row ends in its probe counts
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("jig status lacks %q:\n%s", want, status)
		}
	}
	for _, unwanted := range []string{"skip", "unprobed"} {
		if strings.Contains(status, unwanted) {
			t.Errorf("jig status shows %q:\n%s", unwanted, status)
		}
	}

	keys, frames, wait := startTUI(t, e, ticket, "--store", fx.StoreDir)
	frames.until(t, "round 1 clean")
	keys.Write([]byte("\x1b[A")) // the review step, above the publish one the cursor starts on
	keys.Write([]byte("\r"))
	frames.until(t, "2 probes · 1 caught · 1 survived")
	frames.untilWords(t, "round 1 "+survivor+":1 survived")
	for _, unwanted := range []string{"unprobed", "set aside", "skipped"} {
		if strings.Contains(frames.String(), unwanted) {
			t.Errorf("the review screen shows %q:\n%s", unwanted, frames.String())
		}
	}
	if strings.Contains(frames.String(), "caught in round 2") {
		t.Errorf("a probe is marked superseded before a later round:\n%s", frames.String())
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}

	// Round 2 probes the survivor's line again and catches it.
	changeBeta(t, fx)
	must("gate", ticket)
	req2 := contextOf()
	probe(survivor, 1, "raise the bound again", verifydeliver.ProbeCaught)
	must("review", "done", ticket, "--reviewed", strings.Join(req2.MustReview, ","), "--summary", "probed again")

	keys, frames, wait = startTUI(t, e, ticket, "--store", fx.StoreDir)
	frames.until(t, "round 2 clean")
	keys.Write([]byte("\x1b[A"))
	keys.Write([]byte("\r"))
	frames.until(t, "survived · caught in round 2")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}
}
