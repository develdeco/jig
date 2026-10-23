package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// verifyRig is a fixture with its clone on the ticket's branch and an env that
// runs in the clone and records the processes a command would start.
type verifyRig struct {
	t     *testing.T
	fx    *fixture.Fixture
	st    *store.Store
	spawn *spawnRecorder
	e     env
}

func newVerifyRig(t *testing.T) *verifyRig {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	rec := &spawnRecorder{PID: 777}
	e := testEnv(fx.Home).inDir(fx.RepoDir).withSpawn(rec.Spawn).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))
	r := &verifyRig{t: t, fx: fx, st: st, spawn: rec, e: e}
	gitMust(t, fx.RepoDir, "checkout", "-b", "jig/"+fx.Ticket)
	return r
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
	gitMust(r.t, r.fx.RepoDir, "add", "-A")
	args := []string{"commit", "-m", msg}
	if slice != "" {
		args = append(args, "-m", "Jig-Slice: "+slice)
	}
	gitMust(r.t, r.fx.RepoDir, args...)
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

// TestVerifySplitsItsPositionals: a ticket id and a revision, each optional, in
// either order, neither twice.
func TestVerifySplitsItsPositionals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		pos          []string
		ticket, rev  string
		wantErrMatch string
	}{
		{"none", nil, "", "", ""},
		{"a ticket", []string{"JIG-1"}, "JIG-1", "", ""},
		{"a sha", []string{"abc1234"}, "", "abc1234", ""},
		{"HEAD", []string{"HEAD"}, "", "HEAD", ""},
		{"a ticket and a sha", []string{"JIG-1", "abc1234"}, "JIG-1", "abc1234", ""},
		{"a sha and a ticket", []string{"abc1234", "JIG-1"}, "JIG-1", "abc1234", ""},
		{"a branch named for a ticket is a revision", []string{"JIG-1-fix"}, "", "JIG-1-fix", ""},
		{"two tickets", []string{"JIG-1", "JIG-2"}, "", "", "one ticket"},
		{"two commits", []string{"abc1234", "def5678"}, "", "", "one commit"},
	} {
		ticket, rev, err := splitVerifyArgs(c.pos)
		if c.wantErrMatch != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrMatch) {
				t.Errorf("%s: err = %v, want one saying %q", c.name, err, c.wantErrMatch)
			}
			continue
		}
		if err != nil || ticket != c.ticket || rev != c.rev {
			t.Errorf("%s: = %q, %q, %v, want %q, %q", c.name, ticket, rev, err, c.ticket, c.rev)
		}
	}
}

// TestVerifyStartsADetachedWorkerForTheCommitAtHead: bare `jig verify` in a
// clone on the ticket's branch finds the ticket from the branch, the commit
// from HEAD and the slice from its trailer, starts the worker through the
// spawn seam with its log under the jig home, and says it is pending. It writes
// nothing to the journal itself.
func TestVerifyStartsADetachedWorkerForTheCommitAtHead(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify")
	if code != 0 {
		t.Fatalf("verify exit = %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{"verify:", "ticket: JIG-1", "slice: a", "commit: " + sha, "state: pending", "pid: 777", "help[1]:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	specs := r.spawn.Specs()
	if len(specs) != 1 {
		t.Fatalf("processes started = %d, want 1", len(specs))
	}
	id, err := r.st.ID()
	if err != nil {
		t.Fatal(err)
	}
	wantLog, err := home.VerifyLogPath(r.fx.Home, id, r.fx.Ticket, sha, "a")
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[0]
	wantArgs := []string{"verify", "--worker", "--store", r.st.Root, "--repo-dir", r.fx.RepoDir, "--slice", "a", "JIG-1", sha}
	if !strings.HasSuffix(spec.Path, "jig") || !reflect.DeepEqual(spec.Args, wantArgs) || spec.Dir != r.fx.RepoDir || spec.LogPath != wantLog {
		t.Errorf("Spec = %+v\nwant args %v and log %s", spec, wantArgs, wantLog)
	}
	if !strings.Contains(out, "log: "+wantLog) && !strings.Contains(out, `"`+strings.ReplaceAll(wantLog, `\`, `\`)+`"`) {
		t.Errorf("output does not name the log %s:\n%s", wantLog, out)
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("the foreground wrote journal lines: %+v", lines)
	}
	led, err := ledger.Read(r.fx.Home)
	if err != nil || len(led) != 1 || led[0].Event != journal.EventVerifySpawned || led[0].PID != 777 || led[0].Commit != sha {
		t.Errorf("ledger = %+v, %v, want one verify-spawned line for pid 777", led, err)
	}
}

// TestVerifyReportsAnExistingVerificationAndStartsNothing: a second call finds
// the verification in the journal, whatever state it is in, prints it, and
// starts nothing and writes nothing; --rerun starts a worker over it.
func TestVerifyReportsAnExistingVerificationAndStartsNothing(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Commit: sha, Attempt: 1, Seconds: 4, Summary: "FAIL TestClamp"}); err != nil {
		t.Fatal(err)
	}

	out, code := r.run("verify", "JIG-1", sha[:10])
	if code != 0 {
		t.Fatalf("verify exit = %d, want 0: reporting what is there is not a failure\n%s", code, out)
	}
	for _, want := range []string{"state: fail", "existing: true", "attempt: 1", "--rerun"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := len(r.spawn.Specs()); n != 0 {
		t.Errorf("started %d processes, want none", n)
	}
	if n := len(r.journal()); n != 1 {
		t.Errorf("journal has %d lines, want the 1 the test wrote", n)
	}

	out, code = r.run("verify", "JIG-1", sha, "--rerun")
	if code != 0 || len(r.spawn.Specs()) != 1 || !contains(r.spawn.Specs()[0].Args, "--rerun") || strings.Contains(out, "existing: true") {
		t.Errorf("verify --rerun: exit %d, specs %+v, want a worker started with --rerun\n%s", code, r.spawn.Specs(), out)
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
