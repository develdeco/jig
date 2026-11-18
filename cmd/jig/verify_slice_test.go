package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// lastVerifiedSlice is the slice of the latest oracle line a run journaled.
func lastVerifiedSlice(r *verifyRig) string {
	slice := ""
	for _, l := range r.journal() {
		if l.Event == journal.EventOracle {
			slice = l.Slice
		}
	}
	return slice
}

// TestVerifyPicksTheSliceFromFlagThenTrailerThenClaim is the order the slice
// is chosen in. A flag is the person's word and wins; the commit's trailer is
// what the agent that made it said; a session's claim is the last resort. A
// trailer or claim naming a slice the ticket lacks is passed over.
func TestVerifyPicksTheSliceFromFlagThenTrailerThenClaim(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	claim := func(session, slice string) {
		t.Helper()
		if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: slice, Session: session}); err != nil {
			t.Fatal(err)
		}
	}
	claim("s-1", "c")

	r.commit("b", "b's work", map[string]string{"b.txt": "1"})
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"the flag wins over the trailer", []string{"verify", "JIG-1", "--slice", "d", "--session", "s-1"}, "d"},
		{"the trailer wins over the claim", []string{"verify", "JIG-1", "--session", "s-1"}, "b"},
	} {
		if out, code := r.run(c.args...); code > 1 || lastVerifiedSlice(r) != c.want {
			t.Errorf("%s: exit %d, slice %q, want %q\n%s", c.name, code, lastVerifiedSlice(r), c.want, out)
		}
	}

	r.commit("", "no trailer", map[string]string{"c.txt": "1"})
	if out, code := r.run("verify", "JIG-1", "--session", "s-1"); code > 1 || lastVerifiedSlice(r) != "c" {
		t.Errorf("the claim: exit %d, slice %q, want c\n%s", code, lastVerifiedSlice(r), out)
	}
	// The session comes from JIG_SESSION when the flag is not given.
	r.e.getenv = func(key string) string {
		return map[string]string{"JIG_HOME": r.fx.Home, "JIG_SESSION": "s-1"}[key]
	}
	if out, code := r.run("verify", "JIG-1"); code > 1 || lastVerifiedSlice(r) != "c" {
		t.Errorf("the claim of $JIG_SESSION: exit %d, slice %q, want c\n%s", code, lastVerifiedSlice(r), out)
	}

	// JIG_SLICE names the slice for a commit with no trailer, ahead of a claim.
	r.e.getenv = func(key string) string {
		return map[string]string{"JIG_HOME": r.fx.Home, "JIG_SESSION": "s-1", "JIG_SLICE": "d"}[key]
	}
	if out, code := r.run("verify", "JIG-1"); code > 1 || lastVerifiedSlice(r) != "d" {
		t.Errorf("$JIG_SLICE: exit %d, slice %q, want d\n%s", code, lastVerifiedSlice(r), out)
	}
	r.e.getenv = func(key string) string {
		return map[string]string{"JIG_HOME": r.fx.Home, "JIG_SESSION": "s-1"}[key]
	}

	// A trailer for a slice the ticket does not have is passed over to the claim.
	r.commit("zz", "wrong trailer", map[string]string{"zz.txt": "1"})
	if out, code := r.run("verify", "JIG-1", "--session", "s-1"); code > 1 || lastVerifiedSlice(r) != "c" {
		t.Errorf("a trailer naming no slice: exit %d, slice %q, want the claim c\n%s", code, lastVerifiedSlice(r), out)
	}
}

// TestVerifyRefusesWhatItCannotPlace: no slice to be found, a slice the ticket
// has not got, a commit that does not exist, a branch that names no ticket.
// Each is a validation error that says what to do, and starts nothing.
func TestVerifyRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	r.commit("", "no trailer", map[string]string{"c.txt": "1"})

	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"no slice anywhere", []string{"verify", "JIG-1"}, []string{"cannot tell which slice", "Jig-Slice", "a, b, c, d"}},
		{"a flag for a slice the ticket lacks", []string{"verify", "JIG-1", "--slice", "zz"}, []string{`has no slice "zz"`, "a, b, c, d"}},
		{"a commit, which a run has no place for", []string{"verify", "JIG-1", "0123456789012345678901234567890123456789", "--slice", "a"}, []string{"takes no commit"}},
		{"an option where a ticket goes", []string{"verify", "--all"}, []string{"flag provided but not defined"}},
		{"two tickets", []string{"verify", "JIG-1", "JIG-2"}, []string{"one ticket"}},
		{"an unknown ticket", []string{"verify", "JIG-99"}, []string{"JIG-99"}},
	} {
		out, code := r.run(c.args...)
		if code != 125 {
			t.Errorf("%s: exit = %d, want 125: jig could not start the command\n%s", c.name, code, out)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, want, out)
			}
		}
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("journal has %+v, want nothing", lines)
	}

	// On a branch that names no ticket the ticket must be given.
	gitMust(t, r.fx.RepoDir, "checkout", "-b", "feature/unrelated")
	out, code := r.run("verify")
	if code != 125 || !strings.Contains(out, `names none`) || !strings.Contains(out, "jig verify T-1") {
		t.Errorf("verify on an unrelated branch: exit %d\n%s", code, out)
	}
}

// TestVerifyHelpListsItsFlags: the worker's own flags are gone with the worker.
func TestVerifyHelpListsItsFlags(t *testing.T) {
	t.Parallel()
	got := strings.Join(flagsBlockFor("verify"), "\n")
	for _, want := range []string{"--slice", "--dir", "--timeout", "--dry-run", "--no-record", "--json", "--session", "--store", "--project", "-- <cmd>", "[<ticket>]", "125"} {
		if !strings.Contains(got, want) {
			t.Errorf("jig verify -h lacks %s:\n%s", want, got)
		}
	}
	for _, gone := range []string{"--wait", "--rerun", "--worker", "--agent-id", "--repo-dir"} {
		if strings.Contains(got, gone) {
			t.Errorf("jig verify -h shows %s, which went with the detached worker:\n%s", gone, got)
		}
	}
	out, code := runMain(t, testEnv(t.TempDir()), "", "verify", "-h")
	if code != 0 || !strings.Contains(out, "flags{verify}") {
		t.Errorf("jig verify -h: exit %d\n%s", code, out)
	}
}
