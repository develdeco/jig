package clonebehind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitx.Run(dir, args...); err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

func commit(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.name=f", "-c", "user.email=f@example.invalid", "commit", "-q", "-m", name)
}

// pair is a clone and a second clone used to move origin on.
type pair struct{ origin, clone, other string }

func newPair(t *testing.T) pair {
	t.Helper()
	root := t.TempDir()
	p := pair{
		origin: filepath.Join(root, "origin.git"),
		clone:  filepath.Join(root, "clone"),
		other:  filepath.Join(root, "other"),
	}
	mustGit(t, root, "init", "-q", "--bare", "-b", "main", p.origin)
	mustGit(t, root, "clone", "-q", p.origin, p.clone)
	mustGit(t, p.clone, "checkout", "-q", "-B", "main")
	commit(t, p.clone, "base")
	mustGit(t, p.clone, "push", "-q", "origin", "main")
	mustGit(t, root, "clone", "-q", p.origin, p.other)
	return p
}

// originMoves adds a commit to origin's main and fetches it into the clone.
func (p pair) originMoves(t *testing.T, name string) {
	t.Helper()
	commit(t, p.other, name)
	mustGit(t, p.other, "push", "-q", "origin", "main")
	mustGit(t, p.clone, "fetch", "-q", "origin")
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, p pair) string
		want  State
		behnd int
		ahead int
		noRef bool
	}{
		{"same", func(t *testing.T, p pair) string { return p.clone }, Current, 0, 0, false},
		{"one behind", func(t *testing.T, p pair) string {
			p.originMoves(t, "x")
			return p.clone
		}, Behind, 1, 0, false},
		{"two behind", func(t *testing.T, p pair) string {
			p.originMoves(t, "x")
			p.originMoves(t, "y")
			return p.clone
		}, Behind, 2, 0, false},
		{"ahead", func(t *testing.T, p pair) string {
			commit(t, p.clone, "local")
			return p.clone
		}, Current, 0, 0, false},
		{"diverged", func(t *testing.T, p pair) string {
			commit(t, p.clone, "local")
			p.originMoves(t, "x")
			return p.clone
		}, Diverged, 1, 1, false},
		{"another branch checked out", func(t *testing.T, p pair) string {
			p.originMoves(t, "x")
			mustGit(t, p.clone, "checkout", "-q", "-b", "side")
			return p.clone
		}, Unjudged, 0, 0, false},
		{"detached head", func(t *testing.T, p pair) string {
			p.originMoves(t, "x")
			mustGit(t, p.clone, "checkout", "-q", "--detach")
			return p.clone
		}, Unjudged, 0, 0, false},
		{"no origin ref", func(t *testing.T, p pair) string {
			mustGit(t, p.clone, "update-ref", "-d", "refs/remotes/origin/main")
			return p.clone
		}, Unjudged, 0, 0, true},
		{"no origin remote", func(t *testing.T, p pair) string {
			mustGit(t, p.clone, "remote", "remove", "origin")
			return p.clone
		}, Unjudged, 0, 0, false},
		{"missing directory", func(t *testing.T, p pair) string {
			return filepath.Join(t.TempDir(), "gone")
		}, Unjudged, 0, 0, false},
		{"not a repository", func(t *testing.T, p pair) string { return t.TempDir() }, Unjudged, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newPair(t)
			dir := tc.setup(t, p)
			got := Check(dir, "main")
			if got.State != tc.want || got.NoRef != tc.noRef {
				t.Fatalf("Check = %+v, want state %s noRef %v", got, tc.want, tc.noRef)
			}
			if got.State == Behind || got.State == Diverged {
				if got.Behind != tc.behnd || got.Ahead != tc.ahead || got.Of != "origin/main" {
					t.Fatalf("counts = %+v, want behind %d ahead %d", got, tc.behnd, tc.ahead)
				}
			}
			if wantPull := got.State == Behind; (got.Pull != "") != wantPull {
				t.Fatalf("Pull = %q for state %s", got.Pull, got.State)
			} else if wantPull && got.Pull != "git -C "+Quote(dir)+" pull --ff-only origin main" {
				t.Fatalf("Pull = %q", got.Pull)
			}
			if got.State == Unjudged && got.Reason == "" {
				t.Fatal("an unjudged clone carries no reason")
			}
		})
	}
}

func TestCheckNoRemoteIsNotNoRef(t *testing.T) {
	p := newPair(t)
	mustGit(t, p.clone, "remote", "remove", "origin")
	if got := Check(p.clone, "main"); !got.NoRemote || got.NoRef {
		t.Fatalf("Check = %+v, want NoRemote and not NoRef", got)
	}
}

func TestDivergedNote(t *testing.T) {
	p := newPair(t)
	commit(t, p.clone, "local")
	p.originMoves(t, "x")
	r := Check(p.clone, "main")
	want := "clone " + p.clone + " has diverged from origin/main (as of its last fetch): 1 behind, 1 ahead; a fast-forward pull will refuse, so it needs a merge or rebase by a person"
	if got := DivergedNote(p.clone, r); got != want {
		t.Fatalf("DivergedNote = %q, want %q", got, want)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/work/app":    "/work/app",
		"/work/my app": `"/work/my app"`,
		`C:\work`:      `"C:\work"`,
		`/w/a"b$c`:     `"/w/a\"b\$c"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}
