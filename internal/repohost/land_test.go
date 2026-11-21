package repohost_test

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/repohost"
)

// TestLandPRStatusReadsStateBaseAndMerge: the status is the state, the branch
// it goes into, whether it can merge and, merged, the commit of the merge.
func TestLandPRStatusReadsStateBaseAndMerge(t *testing.T) {
	t.Parallel()
	bin := ghStubBinary(t)
	pulls := fixture.GhPulls(t,
		fixture.GhPull{Number: 1, Base: "jig/I-1"},
		fixture.GhPull{Number: 2, Base: "main", State: "closed", Merged: true, MergeCommit: "deadbeef"},
		fixture.GhPull{Number: 3, Base: "main", Mergeable: "CONFLICTING"},
		fixture.GhPull{Number: 4, Base: "main", State: "closed"},
	)
	env := append(os.Environ(), "GH_STUB_PULLS="+pulls)
	const base = "https://github.example/owner/repo/pull/"
	for _, tc := range []struct {
		n    string
		want repohost.Status
	}{
		{"1", repohost.Status{State: repohost.StateOpen, Mergeable: repohost.MergeableYes, Base: "jig/I-1"}},
		{"2", repohost.Status{State: repohost.StateMerged, Mergeable: repohost.MergeableYes, Base: "main", MergeCommit: "deadbeef"}},
		{"3", repohost.Status{State: repohost.StateOpen, Mergeable: repohost.MergeableConflict, Base: "main"}},
		{"4", repohost.Status{State: repohost.StateClosed, Mergeable: repohost.MergeableYes, Base: "main"}},
	} {
		got, known := repohost.PRStatus(bin, env, base+tc.n)
		if !known || got != tc.want {
			t.Errorf("PRStatus(%s) = %+v, %v, want %+v", tc.n, got, known, tc.want)
		}
	}
	if _, known := repohost.PRStatus(bin, env, base+"9"); known {
		t.Error("a pull request the host does not hold is not known")
	}
	if _, known := repohost.PRStatus(bin, env, "not a url"); known {
		t.Error("a non-URL is not known")
	}
}

// TestLandAllowedMergeMethodsAndPreference: the methods the repo allows come
// from gh, and the preferred one is squash, then merge, then rebase.
func TestLandAllowedMergeMethodsAndPreference(t *testing.T) {
	t.Parallel()
	bin := ghStubBinary(t)
	const url = "https://github.example/owner/repo/pull/1"
	for _, tc := range []struct {
		env       string
		want      repohost.MergeMethods
		preferred string
	}{
		{"", repohost.MergeMethods{Squash: true, Merge: true, Rebase: true}, "squash"},
		{"merge,rebase", repohost.MergeMethods{Merge: true, Rebase: true}, "merge"},
		{"rebase", repohost.MergeMethods{Rebase: true}, "rebase"},
		{"squash", repohost.MergeMethods{Squash: true}, "squash"},
	} {
		env := os.Environ()
		if tc.env != "" {
			env = append(env, "GH_STUB_MERGE_METHODS="+tc.env)
		}
		got, known := repohost.AllowedMergeMethods(bin, env, url)
		if !known || got != tc.want || got.Preferred() != tc.preferred {
			t.Errorf("methods %q = %+v, %v (preferred %q), want %+v (%q)", tc.env, got, known, got.Preferred(), tc.want, tc.preferred)
		}
		if !got.Allows(tc.preferred) || got.Allows("octopus") {
			t.Errorf("methods %q: Allows is wrong for %+v", tc.env, got)
		}
	}
	if m := (repohost.MergeMethods{}); m.Preferred() != "" {
		t.Errorf("no method allowed: preferred = %q, want none", m.Preferred())
	}
	if _, known := repohost.AllowedMergeMethods(bin, os.Environ(), "not a url"); known {
		t.Error("a non-URL names no repo")
	}
}
