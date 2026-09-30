package pool

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestRequireBuilt: a copy that holds every commit jig built passes; one that
// lacks any - on another line, or never seen by this repository - is refused
// with BUILD_LEASE_MISSING, which names the branch, the build lease, how many
// are missing and which, and the command to run on the machine that has them.
func TestRequireBuilt(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	first := pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	own := leaseCommit(t, lease.Dir, "own.txt", "in the lease")
	run(t, lease.Dir, "branch", "elsewhere", first)
	run(t, lease.Dir, "checkout", "elsewhere")
	offBranch := leaseCommit(t, lease.Dir, "off.txt", "on another line")
	run(t, lease.Dir, "checkout", branch)
	const unseen = "0123456789abcdef0123456789abcdef01234567"

	for _, built := range [][]string{nil, {first}, {first, own}, {own, first}} {
		if err := RequireBuilt(lease.Dir, "HEAD", lease.Dir, "T-1", branch, "jig gate T-1", built); err != nil {
			t.Fatalf("RequireBuilt over %v held by the copy: %v", built, err)
		}
	}

	err = RequireBuilt(lease.Dir, "HEAD", "/the/build/lease", "T-1", branch, "jig gate T-1", []string{first, unseen, offBranch, own})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BUILD_LEASE_MISSING" {
		t.Fatalf("RequireBuilt over commits the copy lacks: err = %v, want an *axi.Error BUILD_LEASE_MISSING", err)
	}
	for _, want := range []string{"2 of the 4 commit(s)", branch, "/the/build/lease", unseen[:7] + " " + offBranch[:7]} {
		if !strings.Contains(ae.Msg, want) {
			t.Errorf("BUILD_LEASE_MISSING message %q does not contain %q", ae.Msg, want)
		}
	}
	if strings.Contains(ae.Msg, first[:7]) || strings.Contains(ae.Msg, own[:7]) {
		t.Errorf("BUILD_LEASE_MISSING message %q names a commit the copy holds", ae.Msg)
	}
	help := strings.Join(ae.Help, "\n")
	if !strings.Contains(help, "`jig gate T-1`") {
		t.Errorf("BUILD_LEASE_MISSING help %q does not name the command", help)
	}
	// The commits are found by id: a rebase or squash of them hides them, and
	// the help says to merge instead.
	if !strings.Contains(help, "by commit id") || !strings.Contains(help, "merge") {
		t.Errorf("BUILD_LEASE_MISSING help %q does not say that rewriting the commits hides them and a merge does not", help)
	}

	if err := RequireBuilt(lease.Dir, "refs/heads/nowhere", lease.Dir, "T-1", branch, "jig gate T-1", []string{first}); err == nil {
		t.Fatal("RequireBuilt against a ref that does not exist: expected an error, got nil")
	}
}

// TestHoldsUnpushedBuilt: a copy holds jig's work when it holds a commit jig
// built that origin's copy lacks. One that holds some of them but not all
// still does; one that holds only commits origin has too - the author's
// history, or jig's own once pushed - does not, and neither does one that holds
// none: an attempt's leftovers, a commit this repository has never seen, or
// anything when nothing is built.
func TestHoldsUnpushedBuilt(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	first := pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	own := leaseCommit(t, lease.Dir, "own.txt", "in the lease")
	run(t, lease.Dir, "branch", "elsewhere", first)
	run(t, lease.Dir, "checkout", "elsewhere")
	offBranch := leaseCommit(t, lease.Dir, "off.txt", "on another line")
	run(t, lease.Dir, "checkout", branch)
	pushed := pushCommit(t, remote, branch, "a2.txt", "second") // on origin, not in the lease
	run(t, lease.Dir, "fetch", "origin")
	const unseen = "0123456789abcdef0123456789abcdef01234567"

	for _, tc := range []struct {
		name  string
		built []string
		want  bool
	}{
		{"every one held, one unpushed", []string{first, own}, true},
		{"one held and unpushed, the rest not", []string{unseen, offBranch, own}, true},
		{"only pushed ones held", []string{first, unseen}, false},
		{"held and on origin, not in the lease's copy", []string{first, pushed}, false},
		{"none held", []string{unseen, offBranch}, false},
		{"nothing built", nil, false},
	} {
		got, err := HoldsUnpushedBuilt(lease.Dir, branch, tc.built)
		if err != nil || got != tc.want {
			t.Errorf("%s: HoldsUnpushedBuilt(%v) = %v (err %v), want %v", tc.name, tc.built, got, err, tc.want)
		}
	}

	if _, err := HoldsUnpushedBuilt(lease.Dir, "nowhere", []string{first}); err == nil {
		t.Fatal("HoldsUnpushedBuilt for a branch that does not exist: expected an error, got nil")
	}
}
