package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

const (
	storePersonName  = "Store Person"
	storePersonEmail = "store.person@example.invalid"
	storePersonIdent = storePersonName + " <" + storePersonEmail + ">"

	// commitIdentFormat prints a commit's author and committer.
	commitIdentFormat = "--format=%an <%ae> | %cn <%ce>"
)

// commitIdents lists "author | committer" of every commit in dir after base,
// newest first.
func commitIdents(t *testing.T, dir, base string) []string {
	t.Helper()
	out := gitLog(t, dir, "log", commitIdentFormat, base+"..HEAD")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// requireStorePerson fails the test unless dir has at least one commit after
// base and every one of them has the store person as author and committer.
func requireStorePerson(t *testing.T, what, dir, base string) {
	t.Helper()
	idents := commitIdents(t, dir, base)
	if len(idents) == 0 {
		t.Fatalf("%s made no store commit", what)
	}
	for _, got := range idents {
		if want := storePersonIdent + " | " + storePersonIdent; got != want {
			t.Errorf("%s: a store commit is authored %q, want %q", what, got, want)
		}
	}
}

// TestStoreCommitsCarryCloneIdentity: the store's commits are authored and
// committed by the person whose identity git resolves in the store clone,
// with no author or committer in the environment to say otherwise: what
// ticket new and graduate claim, what requeue pushes after a brief
// amendment, and the leftovers a command's sync records on the way.
func TestStoreCommitsCarryCloneIdentity(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withoutIdentity()
	gitLog(t, fx.StoreDir, "config", "user.name", storePersonName)
	gitLog(t, fx.StoreDir, "config", "user.email", storePersonEmail)

	// ticket new claims a ticket id: a commit, pushed to the store's origin.
	base := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Add retry")
	if r.Code != 0 {
		t.Fatalf("jig ticket new exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	requireStorePerson(t, "ticket new", fx.StoreDir, base)

	// requeue after the brief changed: the amendment is left uncommitted, and
	// the requeue's push records it with the slice state it writes.
	base = gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	briefPath := joinPath(fx.StoreDir, "tickets", fx.Ticket, "brief.md")
	brief := string(readFileOrFatal(t, briefPath))
	amended := strings.Replace(brief, "committed broken", "shipped broken", 1)
	if amended == brief {
		t.Fatal("test setup: the fixture brief has no sentence to amend")
	}
	writeFileOrFatal(t, briefPath, []byte(amended))
	r = runJig(t, env, fx.StoreDir, "requeue", fx.Ticket, "--from-brief-diff")
	if r.Code != 0 {
		t.Fatalf("jig requeue exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	requireStorePerson(t, "requeue", fx.StoreDir, base)

	// graduate claims one ticket per chart entry; the chart itself is left
	// uncommitted for the sync to record.
	base = gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	if err := os.MkdirAll(joinPath(fx.StoreDir, "charts", "mychart"), 0o755); err != nil {
		t.Fatal(err)
	}
	chart := "tickets:\n  - title: Slice A\n  - title: Slice B\n"
	writeFileOrFatal(t, joinPath(fx.StoreDir, "charts", "mychart", "tickets.yaml"), []byte(chart))
	r = runJig(t, env, fx.StoreDir, "graduate", "mychart")
	if r.Code != 0 {
		t.Fatalf("jig graduate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if n := len(commitIdents(t, fx.StoreDir, base)); n < 3 {
		t.Errorf("graduate made %d store commits, want the recorded chart and one claim per entry\nstdout:\n%s", n, r.Stdout)
	}
	requireStorePerson(t, "graduate", fx.StoreDir, base)

	// Nothing the run committed is left dirty or unpushed.
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty after the run:\n%s", status)
	}
	if got, want := gitLog(t, fx.StoreRemote, "rev-parse", "refs/heads/main"), gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin main = %s, want the store HEAD %s", got, want)
	}
}

// TestStoreRefusesWithoutIdentity: a store clone with no identity, and a git
// that may not guess one, refuses a command that must commit: exit 1,
// IDENTITY_REQUIRED with the two commands that set an identity, nothing
// committed and nothing left in the store. A command with nothing to commit
// still runs.
func TestStoreRefusesWithoutIdentity(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withoutIdentity().without("EMAIL")
	gitLog(t, fx.StoreDir, "config", "--unset", "user.name")
	gitLog(t, fx.StoreDir, "config", "--unset", "user.email")
	gitLog(t, fx.StoreDir, "config", "user.useConfigOnly", "true")
	head := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")

	r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Add retry")
	if r.Code != 1 {
		t.Fatalf("jig ticket new exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{
		"IDENTITY_REQUIRED",
		"git config --global user.name",
		"git config --global user.email",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig ticket new stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if got := gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD = %s, want %s: the refused command committed", got, head)
	}
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the refused command left the store dirty:\n%s", status)
	}

	if r := runJig(t, env, fx.StoreDir, "status", fx.Ticket); r.Code != 0 {
		t.Errorf("jig status exit = %d, want 0: nothing to commit needs no identity\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

// TestInitStandaloneIdentity: jig init --standalone commits the new store
// scaffold as the person whose identity git resolves for it (the global
// config, here: the new store has no config of its own), and without one is
// refused before it writes anything, taking the directory it made away
// again.
func TestInitStandaloneIdentity(t *testing.T) {
	t.Parallel()
	newRepo := func(t *testing.T) (env jigEnv, repoDir, storeDir string) {
		t.Helper()
		env = jigEnv{home: t.TempDir(), userHome: t.TempDir()}.withoutIdentity().without("EMAIL").withGitGlobal(t)
		repoDir = filepath.Join(t.TempDir(), "myrepo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		return env, repoDir, repoDir + "-tickets"
	}
	global := func(t *testing.T, env jigEnv, args ...string) {
		t.Helper()
		if _, err := gitx.RunReplaceEnv(t.TempDir(), env.environ(), append([]string{"config", "--global"}, args...)...); err != nil {
			t.Fatalf("git config --global %s: %v", strings.Join(args, " "), err)
		}
	}

	t.Run("commits as the person", func(t *testing.T) {
		t.Parallel()
		env, repoDir, storeDir := newRepo(t)
		global(t, env, "user.name", storePersonName)
		global(t, env, "user.email", storePersonEmail)

		r := runJig(t, env, repoDir, "init", "--standalone")
		if r.Code != 0 {
			t.Fatalf("jig init --standalone exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
		}
		if got, want := gitLog(t, storeDir, "log", "-1", commitIdentFormat), storePersonIdent+" | "+storePersonIdent; got != want {
			t.Errorf("the scaffold commit is authored %q, want %q", got, want)
		}
	})

	t.Run("refused without an identity", func(t *testing.T) {
		t.Parallel()
		env, repoDir, storeDir := newRepo(t)
		global(t, env, "user.useConfigOnly", "true")

		r := runJig(t, env, repoDir, "init", "--standalone")
		if r.Code != 1 {
			t.Fatalf("jig init --standalone exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
		}
		for _, want := range []string{"IDENTITY_REQUIRED", "git config --global user.name", "git config --global user.email"} {
			if !strings.Contains(r.Stdout, want) {
				t.Errorf("jig init --standalone stdout lacks %q:\n%s", want, r.Stdout)
			}
		}
		if _, err := os.Stat(storeDir); !os.IsNotExist(err) {
			t.Errorf("the refused init left %s behind: %v", storeDir, err)
		}
	})
}
