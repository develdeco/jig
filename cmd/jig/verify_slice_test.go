package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// lastSpawnedSlice is the slice of the worker the last spawn started.
func lastSpawnedSlice(r *verifyRig) string {
	specs := r.spawn.Specs()
	if len(specs) == 0 {
		return ""
	}
	args := specs[len(specs)-1].Args
	for i, a := range args {
		if a == "--slice" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
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

	trailered := r.commit("b", "b's work", map[string]string{"b.txt": "1"})
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"the flag wins over the trailer", []string{"verify", "JIG-1", trailered, "--slice", "d", "--session", "s-1"}, "d"},
		{"the trailer wins over the claim", []string{"verify", "JIG-1", trailered, "--session", "s-1"}, "b"},
	} {
		if out, code := r.run(c.args...); code != 0 || lastSpawnedSlice(r) != c.want {
			t.Errorf("%s: exit %d, slice %q, want %q\n%s", c.name, code, lastSpawnedSlice(r), c.want, out)
		}
	}

	bare := r.commit("", "no trailer", map[string]string{"c.txt": "1"})
	if out, code := r.run("verify", "JIG-1", bare, "--session", "s-1"); code != 0 || lastSpawnedSlice(r) != "c" {
		t.Errorf("the claim: exit %d, slice %q, want c\n%s", code, lastSpawnedSlice(r), out)
	}
	// The session comes from JIG_SESSION when the flag is not given.
	r.e.getenv = func(key string) string {
		return map[string]string{"JIG_HOME": r.fx.Home, "JIG_SESSION": "s-1"}[key]
	}
	if out, code := r.run("verify", "JIG-1", bare); code != 0 || lastSpawnedSlice(r) != "c" {
		t.Errorf("the claim of $JIG_SESSION: exit %d, slice %q, want c\n%s", code, lastSpawnedSlice(r), out)
	}

	// A trailer for a slice the ticket does not have is passed over to the claim.
	wrong := r.commit("zz", "wrong trailer", map[string]string{"zz.txt": "1"})
	if out, code := r.run("verify", "JIG-1", wrong, "--session", "s-1"); code != 0 || lastSpawnedSlice(r) != "c" {
		t.Errorf("a trailer naming no slice: exit %d, slice %q, want the claim c\n%s", code, lastSpawnedSlice(r), out)
	}
}

// TestVerifyRefusesWhatItCannotPlace: no slice to be found, a slice the ticket
// has not got, a commit that does not exist, a branch that names no ticket.
// Each is a validation error that says what to do, and starts nothing.
func TestVerifyRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	bare := r.commit("", "no trailer", map[string]string{"c.txt": "1"})
	trailered := r.fixClamp()

	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"no slice anywhere", []string{"verify", "JIG-1", bare}, []string{"cannot tell which slice", "Jig-Slice", "a, b, c, d"}},
		{"a flag for a slice the ticket lacks", []string{"verify", "JIG-1", trailered, "--slice", "zz"}, []string{`has no slice "zz"`, "a, b, c, d"}},
		{"a commit that is not there", []string{"verify", "JIG-1", "0123456789012345678901234567890123456789", "--slice", "a"}, []string{"is not in"}},
		{"an option where a commit goes", []string{"verify", "JIG-1", "--", "--all"}, []string{"is not a commit"}},
		{"two tickets", []string{"verify", "JIG-1", "JIG-2"}, []string{"one ticket"}},
		{"an unknown ticket", []string{"verify", "JIG-99", trailered}, []string{"JIG-99"}},
	} {
		out, code := r.run(c.args...)
		if code != 2 && code != 1 {
			t.Errorf("%s: exit = %d, want a refusal\n%s", c.name, code, out)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, want, out)
			}
		}
	}
	if n := len(r.spawn.Specs()); n != 0 {
		t.Errorf("started %d processes, want none", n)
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("journal has %+v, want nothing", lines)
	}

	// On a branch that names no ticket the ticket must be given.
	gitMust(t, r.fx.RepoDir, "checkout", "-b", "feature/unrelated")
	out, code := r.run("verify")
	if code != 2 || !strings.Contains(out, `names none`) || !strings.Contains(out, "jig verify T-1") {
		t.Errorf("verify on an unrelated branch: exit %d\n%s", code, out)
	}
}

// TestVerifyHelpListsItsFlagsButNotTheWorkersOwn: --worker, --agent and
// --clone are how the foreground talks to the worker; the person reads about
// the rest.
func TestVerifyHelpListsItsFlagsButNotTheWorkersOwn(t *testing.T) {
	t.Parallel()
	got := strings.Join(flagsBlockFor("verify"), "\n")
	for _, want := range []string{"--slice", "--wait", "--rerun", "--session", "--store", "--project"} {
		if !strings.Contains(got, want) {
			t.Errorf("jig verify -h lacks %s:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"--worker", "--agent", "--repo-dir"} {
		if strings.Contains(got, hidden) {
			t.Errorf("jig verify -h shows the hidden %s:\n%s", hidden, got)
		}
	}
	out, code := runMain(t, testEnv(t.TempDir()), "", "verify", "-h")
	if code != 0 || !strings.Contains(out, "flags{verify}") {
		t.Errorf("jig verify -h: exit %d\n%s", code, out)
	}
}
