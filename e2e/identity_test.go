package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx/gitxtest"
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
		if _, err := gitxtest.RunReplaceEnv(t.TempDir(), env.environ(), append([]string{"config", "--global"}, args...)...); err != nil {
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
