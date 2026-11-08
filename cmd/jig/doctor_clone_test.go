package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/clonebehind"
	"github.com/develdeco/jig/internal/gitx"
)

// cloneFx is a marked project whose clone has a bare origin and a second
// clone of that origin to move it on.
type cloneFx struct {
	repo, origin, other string
	git                 func(dir string, args ...string)
}

func newCloneFx(t *testing.T, ie installerEnv, marker string) cloneFx {
	t.Helper()
	repo := ie.markedProject(t, "app", marker)
	f := cloneFx{repo: repo, origin: filepath.Join(ie.root, "origin.git"), other: filepath.Join(ie.root, "other")}
	f.git = func(dir string, args ...string) {
		t.Helper()
		args = append([]string{"-c", "user.name=f", "-c", "user.email=f@example.invalid"}, args...)
		if _, err := gitx.RunEnv(dir, ie.gitEnv(), args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	f.git(ie.root, "init", "-q", "--bare", "-b", "main", f.origin)
	f.git(repo, "add", "-A")
	f.git(repo, "commit", "-q", "-m", "base")
	f.git(repo, "remote", "add", "origin", f.origin)
	f.git(repo, "push", "-q", "-u", "origin", "main")
	f.git(ie.root, "clone", "-q", f.origin, f.other)
	return f
}

// originMoves adds a commit to origin's branch and fetches it into the clone.
func (f cloneFx) originMoves(t *testing.T, branch, name string) {
	t.Helper()
	f.git(f.other, "fetch", "-q", "origin")
	f.git(f.other, "checkout", "-q", "-B", branch, "origin/"+branch)
	writeTestFile(t, filepath.Join(f.other, name), name+"\n")
	f.git(f.other, "add", "-A")
	f.git(f.other, "commit", "-q", "-m", name)
	f.git(f.other, "push", "-q", "origin", branch)
	f.git(f.repo, "fetch", "-q", "origin")
}

// TestDoctorNamesAClonePastItsOrigin: a clone one commit behind origin's
// target is named with the pull, from the clone and from a linked worktree of
// it, and is not once the pull has been run.
func TestDoctorNamesAClonePastItsOrigin(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\n")
	e := ie.env.inDir(f.repo)

	out, _ := runMain(t, e, "", "doctor")
	if strings.Contains(out, "behind") {
		t.Errorf("an up-to-date clone is named:\n%s", out)
	}

	f.originMoves(t, "main", "x.txt")
	worktree := filepath.Join(ie.root, "app.wt")
	f.git(f.repo, "worktree", "add", "-q", "-b", "feat", worktree)

	want := "clone is 1 commit behind origin/main (as of its last fetch): git -C " + clonebehind.Quote(f.repo) + " pull --ff-only origin main"
	for name, dir := range map[string]string{"the clone": f.repo, "a linked worktree": worktree} {
		out, _ = runMain(t, ie.env.inDir(dir), "", "doctor")
		if !strings.Contains(out, want) {
			t.Errorf("from %s:\n%s\nwant a note %q", name, out, want)
		}
	}

	f.git(f.repo, "pull", "-q", "--ff-only", "origin", "main")
	out, _ = runMain(t, e, "", "doctor")
	if strings.Contains(out, "behind") {
		t.Errorf("a clone after its pull is named:\n%s", out)
	}
}

// TestDoctorJudgesTheMarkersTarget: a project that lands on another branch
// has its clone judged against that branch's origin ref.
func TestDoctorJudgesTheMarkersTarget(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\nrepos:\n  - name: app\n    target: develop\n")
	f.git(f.repo, "push", "-q", "origin", "main:develop")
	f.git(f.repo, "fetch", "-q", "origin")
	f.git(f.repo, "checkout", "-q", "-B", "develop", "origin/develop")
	f.originMoves(t, "develop", "d.txt")
	out, _ := runMain(t, ie.env.inDir(f.repo), "", "doctor")
	want := "clone is 1 commit behind origin/develop (as of its last fetch): git -C " + clonebehind.Quote(f.repo) + " pull --ff-only origin develop"
	if !strings.Contains(out, want) {
		t.Errorf("a clone behind its marker's target:\n%s\nwant %q", out, want)
	}
}

// TestDoctorSaysWhenItCannotTell: the diverged line gives both counts and no
// command, a missing origin ref says to fetch, and a missing origin remote
// does not.
func TestDoctorSaysWhenItCannotTell(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\n")
	e := ie.env.inDir(f.repo)

	writeTestFile(t, filepath.Join(f.repo, "local.txt"), "l\n")
	f.git(f.repo, "add", "-A")
	f.git(f.repo, "commit", "-q", "-m", "local")
	f.originMoves(t, "main", "x.txt")
	out, _ := runMain(t, e, "", "doctor")
	if !strings.Contains(out, "has diverged from origin/main (as of its last fetch): 1 behind, 1 ahead; a fast-forward pull will refuse") || strings.Contains(out, "pull --ff-only") {
		t.Errorf("a diverged clone:\n%s", out)
	}

	f.git(f.repo, "update-ref", "-d", "refs/remotes/origin/main")
	out, _ = runMain(t, e, "", "doctor")
	if want := "has no origin/main; run git -C " + clonebehind.Quote(f.repo) + " fetch origin"; !strings.Contains(out, "cannot tell whether") || !strings.Contains(out, want) {
		t.Errorf("a clone with no origin ref:\n%s", out)
	}

	f.git(f.repo, "remote", "remove", "origin")
	out, _ = runMain(t, e, "", "doctor")
	if !strings.Contains(out, "it has no origin remote") || strings.Contains(out, "fetch origin") {
		t.Errorf("a clone with no origin remote:\n%s", out)
	}
}
