package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitbuild"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// verifyRig is a fixture with its clone on the ticket's branch and an env that
// runs in the clone and records the processes a command would start.
type verifyRig struct {
	t  *testing.T
	fx *fixture.Fixture
	st *store.Store
	e  env
}

func newVerifyRig(t *testing.T) *verifyRig {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	e := testEnv(fx.Home).inDir(fx.RepoDir).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))
	r := &verifyRig{t: t, fx: fx, st: st, e: e}
	gitMust(t, fx.RepoDir, "checkout", "-b", "jig/"+fx.Ticket)
	return r
}

// gitBuilder is the Builder for a test's own commits: in process where gitx
// serves the repository, the git program where it declines, under the identity
// and dates TestMain pinned.
func gitBuilder(t testing.TB) *gitbuild.Builder {
	t.Helper()
	return gitbuild.New(t, nil)
}

// gitMust runs git in dir and fails the test on an error.
func gitMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// commit commits files in the clone, with the Jig-Slice trailer when slice is
// not empty, and returns the sha.
func (r *verifyRig) commit(slice, msg string, files map[string]string) string {
	r.t.Helper()
	for rel, content := range files {
		full := filepath.Join(r.fx.RepoDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	if slice != "" {
		msg += "\n\nJig-Slice: " + slice
	}
	gitBuilder(r.t).Commit(r.fx.RepoDir, msg)
	return gitMust(r.t, r.fx.RepoDir, "rev-parse", "HEAD")
}

// fixClamp commits the change slice a is about.
func (r *verifyRig) fixClamp() string {
	r.t.Helper()
	path := filepath.Join(r.fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	fixed := strings.Replace(string(data), "\treturn v\n", "\tif v > hi {\n\t\treturn hi\n\t}\n\treturn v\n", 1)
	return r.commit("a", "fix Clamp", map[string]string{"alpha/alpha.go": fixed})
}

func (r *verifyRig) journal() []journal.Line {
	r.t.Helper()
	lines, err := journal.Read(r.st, r.fx.Ticket)
	if err != nil {
		r.t.Fatal(err)
	}
	return lines
}

func (r *verifyRig) run(args ...string) (string, int) {
	r.t.Helper()
	return runMain(r.t, r.e, "", args...)
}

// TestVerifySplitsItsPositionals: a ticket id, optional and given once. A run
// is of the worktree's HEAD, so a commit is no positional.
func TestVerifySplitsItsPositionals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		pos          []string
		ticket       string
		wantErrMatch string
	}{
		{"none", nil, "", ""},
		{"a ticket", []string{"JIG-1"}, "JIG-1", ""},
		{"a sha", []string{"abc1234"}, "", "takes no commit"},
		{"HEAD", []string{"HEAD"}, "", "takes no commit"},
		{"a ticket and a sha", []string{"JIG-1", "abc1234"}, "", "takes no commit"},
		{"a branch named for a ticket is no ticket", []string{"JIG-1-fix"}, "", "takes no commit"},
		{"two tickets", []string{"JIG-1", "JIG-2"}, "", "one ticket"},
		{"an option", []string{"--all"}, "", "is not a ticket"},
	} {
		ticket, err := splitVerifyArgs(c.pos)
		if c.wantErrMatch != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrMatch) {
				t.Errorf("%s: err = %v, want one saying %q", c.name, err, c.wantErrMatch)
			}
			continue
		}
		if err != nil || ticket != c.ticket {
			t.Errorf("%s: = %q, %v, want %q", c.name, ticket, err, c.ticket)
		}
	}
}

// TestVerifyStartsNoWorkerForTheCommitAtHead: bare `jig verify` in a clone on
// the ticket's branch finds the ticket from the branch, the commit from HEAD
// and the slice from its trailer, and runs the oracle in this process: it
// starts no process of jig's and writes no verify-spawned line.
func TestVerifyStartsNoWorkerForTheCommitAtHead(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify")
	if code != 0 {
		t.Fatalf("verify exit = %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{"verify:", "ticket: JIG-1", "slice: a", "commit: " + sha, "state: pass", "covers: yes", "help[1]:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var kinds []string
	for _, l := range r.journal() {
		kinds = append(kinds, l.Event)
	}
	if want := []string{"oracle", "verified"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("journal events = %v, want %v", kinds, want)
	}
	led, err := ledger.Read(r.fx.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range led {
		if l.Event == journal.EventVerifySpawned {
			t.Errorf("ledger holds a verify-spawned line: %+v", l)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
