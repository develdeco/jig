package verifydeliver

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
)

// ambientWithoutIdentity returns this test binary's own process environment
// with the six GIT_AUTHOR_*/GIT_COMMITTER_* variables this package's
// TestMain pins for every other test's commits (gittest.PinIdentity)
// dropped: Deps.GitEnv carries it into Publish's identity resolution so
// that pin cannot shadow the mapped clone's own config, or its absence,
// without editing the process environment to get there.
func ambientWithoutIdentity(t *testing.T) []string {
	t.Helper()
	drop := map[string]bool{
		"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
		"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
	}
	var kept []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			kept = append(kept, kv)
		}
	}
	return kept
}

// identityRepo creates a bare-bones git repo at a fresh temp dir with
// user.name/user.email configured repo-locally, standing in for an
// operator's own mapped clone.
func identityRepo(t *testing.T, name, email string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := gitx.Run(dir, "config", "user.name", name); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := gitx.Run(dir, "config", "user.email", email); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	return dir
}

// TestPublishCommitsWithMappedCloneIdentity is the identity-resolution
// regression: it registers a Machine.Clones entry for the fixture repo
// pointing at a directory with its own distinct repo-local identity -
// never the fixture identity this package's TestMain pins process-wide -
// and checks that identity, not the pinned one, lands on the squash
// commit. d.GitEnv strips that pin from Publish's own identity resolution,
// so the mapped clone's config decides instead.
func TestPublishCommitsWithMappedCloneIdentity(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	d.GitEnv = ambientWithoutIdentity(t)

	operatorDir := identityRepo(t, "Operator Distinct", "operator@example.invalid")
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": operatorDir}}

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	sha, ok := report.Squashed["fixture-repo"]
	if !ok || sha == "" {
		t.Fatalf("Squashed[fixture-repo] missing, got %v", report.Squashed)
	}

	leaseDir := publishLeaseDir(t, fx)
	got, err := gitx.Run(leaseDir, "log", "-1", "--format=%an <%ae> / %cn <%ce>", sha)
	if err != nil {
		t.Fatalf("log squash identity: %v", err)
	}
	want := "Operator Distinct <operator@example.invalid> / Operator Distinct <operator@example.invalid>"
	if got != want {
		t.Fatalf("squash commit identity = %q, want %q", got, want)
	}
}

// TestPublishFailsIdentityRequiredAndPushesNothing checks the other side of
// the same gate: with no identity resolvable anywhere - no mapped clone
// registered, so Publish falls back to its lease, and that lease has
// user.useConfigOnly sealed onto it (below) so git can neither find an
// identity there nor guess one from the host's own user account or
// hostname - it fails IDENTITY_REQUIRED before its first commit and pushes
// nothing to the ticket branch. d.GitEnv strips this package's pinned
// identity from Publish's own resolution the same way the test above does,
// so nothing papers over the lease's missing identity.
func TestPublishFailsIdentityRequiredAndPushesNothing(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	d.GitEnv = ambientWithoutIdentity(t)
	// d.Machine is left zero-valued: no mapped clone recorded for this
	// project on this machine, so Publish falls back to checking its own
	// (identity-less) lease.
	sealNoIdentityOntoPublishLease(t, fx)

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("Publish err = %v (%T), want *axi.Error", err, err)
	}
	if ae.Code != "IDENTITY_REQUIRED" {
		t.Fatalf("code = %q, want IDENTITY_REQUIRED", ae.Code)
	}
	if strings.Count(ae.Msg, "\n") != 0 {
		t.Fatalf("Msg = %q, want a single line", ae.Msg)
	}

	if _, err := gitx.Run(fx.RepoRemote, "rev-parse", "--verify", "--quiet", "refs/heads/"+ticketBranch(fx.Ticket)); err == nil {
		t.Fatalf("refs/heads/%s exists on origin, want nothing pushed", ticketBranch(fx.Ticket))
	}
}

// sealNoIdentityOntoPublishLease acquires the ticket's publish lease once,
// ahead of Publish, and sets user.useConfigOnly on it, repo-locally: git
// then refuses to guess an identity from the host's own user account or
// hostname the way it otherwise would with none configured, so the lease
// carries no identity deterministically, on any host. Publish's own
// Acquire, right after, reuses this same lease (a fetch, never a fresh
// clone, once a directory is already its own repository - see
// pool.Acquire), so the config set here is still there when Publish
// resolves identity immediately afterward.
func sealNoIdentityOntoPublishLease(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Publish)
	if err != nil {
		t.Fatalf("pool.Acquire publish lease: %v", err)
	}
	if _, err := gitx.Run(lease.Dir, "config", "user.useConfigOnly", "true"); err != nil {
		t.Fatalf("git config user.useConfigOnly: %v", err)
	}
}
