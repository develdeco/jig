package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// unsetIdentityEnvForTest unsets every GIT_AUTHOR_*/GIT_COMMITTER_* variable
// gittest.PinIdentity set process-wide (via this package's TestMain), for
// the duration of one test, restoring each to its pinned value afterward.
// A test that wants to prove identity resolves from somewhere other than
// the ambient environment - a distinct operator identity, or no identity at
// all - needs this: env always wins over any git config, pinned or not.
func unsetIdentityEnvForTest(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	} {
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
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
// never the fixture identity this package's TestMain pins process-wide,
// and never any identity the pool lease itself carries - and checks that
// identity, not the pinned one, lands on the squash commit. Without
// resolving identity from the operator's own mapped clone (see the package
// doc), this would land the pinned/ambient identity instead, exactly the
// bug that motivated the fix.
func TestPublishCommitsWithMappedCloneIdentity(t *testing.T) {
	unsetIdentityEnvForTest(t)
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

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

	leaseDir := filepath.Join(mustPoolDir(t), "fixture-repo", fx.Ticket+"-publish")
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
// the same gate: with no identity resolvable anywhere (no mapped clone
// registered, so Publish falls back to its lease - a fresh clone with no
// identity of its own - and the ambient environment has none either), it
// fails IDENTITY_REQUIRED before its first commit and pushes nothing to
// the ticket branch.
func TestPublishFailsIdentityRequiredAndPushesNothing(t *testing.T) {
	unsetIdentityEnvForTest(t)
	t.Setenv("JIG_HOME", t.TempDir())

	cfgPath := filepath.Join(t.TempDir(), "gitconfig-no-identity")
	if err := os.WriteFile(cfgPath, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfgPath)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	fx := fixture.Generate(t, fixture.Opts{})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	// d.Machine is left zero-valued: no mapped clone recorded for this
	// project on this machine, so Publish falls back to checking its own
	// (identity-less) lease.

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
