package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// statesRig is the fixture clone on its ticket's branch with the ticket's
// start sha recorded, as a claim records it.
func statesRig(t *testing.T) (*fixture.Fixture, env) {
	t.Helper()
	fx, e, _ := observeRig(t)
	start, err := gitx.RevParse(fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStartSHA(fx.Ticket, "fixture-repo", start); err != nil {
		t.Fatal(err)
	}
	return fx, e
}

// commitWithTrailer commits a file in the clone with the slice trailer, as a
// builder does while no jig is on PATH: nothing verifies it.
func commitWithTrailer(t *testing.T, fx *fixture.Fixture, file, slice string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.RepoDir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", "add " + file, "-m", "Jig-Slice: " + slice}} {
		if _, err := gitx.Run(fx.RepoDir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := gitx.RevParse(fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

type statesJSON struct {
	State struct {
		VerificationStates []struct {
			Slice, Commit, State, Told, Clear string
		} `json:"verification_states"`
	} `json:"state"`
}

// TestHereNamesVerificationStates: a commit made with the slice trailer while
// no jig ran has no verification, and `jig here` names it not verified with
// the command that verifies it, in the text and in --json.
func TestHereNamesVerificationStates(t *testing.T) {
	t.Parallel()
	fx, e := statesRig(t)
	sha := commitWithTrailer(t, fx, "one.txt", "a")

	out, code := runMain(t, e, "", "here")
	if code != 0 {
		t.Fatalf("jig here: exit %d, output %q", code, out)
	}
	want := "jig verify " + fx.Ticket + " " + sha[:12] + " --slice a --wait"
	if !strings.Contains(out, "verification_states[1]") || !strings.Contains(out, "not verified") || !strings.Contains(out, want) {
		t.Errorf("jig here does not name the unverified commit with %q:\n%s", want, out)
	}

	out, code = runMain(t, e, "", "here", "--json")
	if code != 0 {
		t.Fatalf("jig here --json: exit %d, output %q", code, out)
	}
	var got statesJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	rows := got.State.VerificationStates
	if len(rows) != 1 || rows[0].Slice != "a" || rows[0].Commit != sha || rows[0].State != "not verified" || rows[0].Clear != want {
		t.Errorf("verification_states = %+v, want the one unverified commit", rows)
	}
}

// TestStatusNamesVerificationStates: `jig status` tells the same commit, and
// says the check was not done when the branch cannot be read.
func TestStatusNamesVerificationStates(t *testing.T) {
	t.Parallel()
	fx, e := statesRig(t)
	sha := commitWithTrailer(t, fx, "one.txt", "a")
	// The ticket's own branch, as `jig next` names it, holds the commit.
	next, ncode := runMain(t, e, "", "next", fx.Ticket)
	b := regexp.MustCompile(`(?m)^\s*branch:\s*(\S+)`).FindStringSubmatch(next)
	if ncode != 0 || b == nil {
		t.Fatalf("next: exit %d, no branch in %q", ncode, next)
	}
	if _, err := gitx.Run(fx.RepoDir, "branch", b[1], "HEAD"); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, e, "", "status", fx.Ticket)
	if code != 0 {
		t.Fatalf("jig status: exit %d, output %q", code, out)
	}
	if !strings.Contains(out, "verification_states[1]") || !strings.Contains(out, "not verified") || !strings.Contains(out, sha[:12]) {
		t.Errorf("jig status does not name the unverified commit:\n%s", out)
	}

	// With no start sha there is no branch to read: the check is not done.
	if err := os.Remove(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "start.fixture-repo.sha")); err != nil {
		t.Fatal(err)
	}
	out, _ = runMain(t, e, "", "status", fx.Ticket)
	if !strings.Contains(out, "not verified: not checked (") || strings.Contains(out, "verification_states[") {
		t.Errorf("jig status without a branch to read should say the check was not done:\n%s", out)
	}
}

// TestHereVerificationStatesCostOneGitLog: finding the unverified commits
// costs one git program however many commits the branch holds.
func TestHereVerificationStatesCostOneGitLog(t *testing.T) {
	fx, e := startPathRig(t)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	startPath := st.StartSHAPath(fx.Ticket, "fixture-repo")
	_ = os.Remove(startPath)
	run := func() int {
		processes, _ := measureWork(t, func() {
			if out, code := runMain(t, e, "", "here"); code != 0 {
				t.Fatalf("jig here: exit %d, output %q", code, out)
			}
		})
		return processes
	}
	without := run()

	start, err := gitx.RevParse(fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStartSHA(fx.Ticket, "fixture-repo", start); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"c1.txt", "c2.txt", "c3.txt", "c4.txt", "c5.txt"} {
		commitWithTrailer(t, fx, f, "a")
	}
	with := run()
	t.Logf("jig here: %d git processes without a branch to read, %d with five unverified commits", without, with)
	if with > without+1 {
		t.Errorf("reading five commits cost %d more git processes, want at most one", with-without)
	}
}
