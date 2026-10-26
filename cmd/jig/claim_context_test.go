package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestClaimStatesAndHolders: a claim makes the slice building once however
// often the same session makes it; a green slice cannot be claimed; a slice
// parked on a question stays parked under a claim; another session finds a
// held slice held until it takes it; a release of what nobody holds writes
// nothing.
func TestClaimStatesAndHolders(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket

	for i := 0; i < 2; i++ {
		out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-1")
		want := []string{"result: claimed\n", "result: already-claimed\n"}[i]
		if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, "state: building\n") {
			t.Fatalf("claim %d: exit %d, want %q\n%s", i+1, code, want, out)
		}
	}
	if lines := linesOf(t, st, T, journal.EventClaim); len(lines) != 1 || lines[0].Session != "s-1" || lines[0].Slice != "a" {
		t.Errorf("claim lines = %+v, want the one", lines)
	}
	if out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-2"); code != 1 || !strings.Contains(out, "SLICE_HELD") {
		t.Errorf("claim by another session: exit %d\n%s", code, out)
	}

	if err := st.WriteSliceState(T, "b", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "claim", T, "--slice", "b", "--session", "s-1"); code != 1 || !strings.Contains(out, "SLICE_GREEN") {
		t.Errorf("claim of a green slice: exit %d\n%s", code, out)
	}

	runMain(t, e, "", "ask", T, "--slice", "c", "which?")
	out, code := runMain(t, e, "", "claim", T, "--slice", "c", "--session", "s-1")
	if ss, _ := st.ReadSliceState(T, "c"); code != 0 || ss.State != "needs-input" || !strings.Contains(out, "state: needs-input\n") {
		t.Errorf("claim of a parked slice: exit %d, state %+v\n%s", code, ss, out)
	}

	before := len(linesOf(t, st, T, journal.EventRelease))
	if out, code := runMain(t, e, "", "claim", T, "--slice", "d", "--release"); code != 0 || !strings.Contains(out, "result: not-claimed\n") {
		t.Errorf("release of an unclaimed slice: exit %d\n%s", code, out)
	}
	if after := len(linesOf(t, st, T, journal.EventRelease)); after != before {
		t.Errorf("a release of what nobody held wrote %d release lines", after-before)
	}
	if out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--release", "--session", "s-1"); code != 0 || !strings.Contains(out, "state: queued\n") {
		t.Errorf("release of a: exit %d\n%s", code, out)
	}
}

// TestClaimWithAnUnattendedMarker: a jig.yaml marker whose unattended is on
// makes every claim in the clone one made with nobody watching, without the
// flag.
func TestClaimWithAnUnattendedMarker(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	marker := "jig: 1\nproject: fixture\nunattended: true\n"
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "jig.yaml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fixture brief has no approval, so an unattended claim is refused.
	out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", "a")
	if code != 1 || !strings.Contains(out, "code: CLAIM_NOT_APPROVED") {
		t.Fatalf("claim under an unattended marker: exit %d\n%s", code, out)
	}
	if lines := linesOf(t, st, fx.Ticket, journal.EventClaim); len(lines) != 0 {
		t.Errorf("a refused claim wrote %+v", lines)
	}
}

// TestContextSliceSources: the slice of a context is --slice, then JIG_SLICE,
// then the one the session claimed; none is a refusal naming the slices, and a
// slice the ticket does not have is SLICE_NOT_FOUND. --json is one object.
func TestContextSliceSources(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	T := fx.Ticket

	if out, code := runMain(t, e, "", "context", T); code != 2 || !strings.Contains(out, "no slice") || !strings.Contains(out, "a, b, c, d") {
		t.Errorf("context with no slice: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "context", T, "--slice", "zz"); code != 1 || !strings.Contains(out, "SLICE_NOT_FOUND") {
		t.Errorf("context of a missing slice: exit %d\n%s", code, out)
	}

	slice := func(out string) string {
		var c struct {
			Slice struct{ ID string }
		}
		if err := json.Unmarshal([]byte(out), &c); err != nil {
			t.Fatalf("not one JSON object: %v\n%s", err, out)
		}
		return c.Slice.ID
	}
	out, code := runMain(t, e, "", "context", T, "--slice", "c", "--json")
	if code != 0 || slice(out) != "c" {
		t.Fatalf("context --slice c --json: exit %d\n%s", code, out)
	}
	withEnv := envFrom(map[string]string{"JIG_HOME": fx.Home, "JIG_SLICE": "d", "JIG_SESSION": "s-7"}).inDir(fx.RepoDir)
	if out, code := runMain(t, withEnv, "", "context", T, "--json"); code != 0 || slice(out) != "d" {
		t.Errorf("context with JIG_SLICE=d: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, withEnv, "", "context", T, "--slice", "b", "--json"); code != 0 || slice(out) != "b" {
		t.Errorf("--slice over JIG_SLICE: exit %d\n%s", code, out)
	}
	claimEnv := envFrom(map[string]string{"JIG_HOME": fx.Home, "JIG_SESSION": "s-7"}).inDir(fx.RepoDir)
	runMain(t, claimEnv, "", "claim", T, "--slice", "a")
	if out, code := runMain(t, claimEnv, "", "context", T, "--json"); code != 0 || slice(out) != "a" {
		t.Errorf("context after the session claimed a: exit %d\n%s", code, out)
	}
}

// TestContextRunsNoContextTool: jig context looks up no program, even in a
// store whose config still names a code graph under context: (ADR 0034). It
// prints no related block and its --json has no related key; its help says
// the code is the session's to find with its own tools, and points at the
// files the earlier slices changed once a slice has verified.
func TestContextRunsNoContextTool(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	cfg := filepath.Join(fx.StoreDir, "project.yaml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(data, "context:\n  graphify: true\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	var looked []string
	e.lookPath = func(file string) (string, error) {
		looked = append(looked, file)
		return "", errors.New("lookPath not expected")
	}
	const own = "Find the code the goal touches with your own tools"
	const fromEarlier = "starting from the files the earlier slices changed"

	out, code := runMain(t, e, "", "context", T, "--slice", "b")
	if code != 0 || !strings.Contains(out, own) || strings.Contains(out, fromEarlier) || strings.Contains(out, "related[") {
		t.Errorf("context before any slice verified: exit %d, want the advice with no earlier files and no related block\n%s", code, out)
	}

	if err := journal.Append(st, T, journal.Line{Slice: "a", Event: journal.EventVerified, Commit: strings.Repeat("a", 40), Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	out, code = runMain(t, e, "", "context", T, "--slice", "b")
	if code != 0 || !strings.Contains(out, own+" (search, a code graph, your editor's symbols), "+fromEarlier) {
		t.Errorf("context after slice a verified: exit %d, want the advice pointing at the earlier slices' files\n%s", code, out)
	}
	out, code = runMain(t, e, "", "context", T, "--slice", "b", "--json")
	var c map[string]any
	if err := json.Unmarshal([]byte(out), &c); code != 0 || err != nil {
		t.Fatalf("context --json: exit %d, %v\n%s", code, err, out)
	}
	if _, ok := c["related"]; ok {
		t.Errorf("context --json has a related key: %s", out)
	}
	if len(looked) != 0 {
		t.Errorf("jig context looked up %v, want no program", looked)
	}
}
