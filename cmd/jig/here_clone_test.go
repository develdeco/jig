package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/clonebehind"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
)

func cloneCtx(f cloneFx) hereCtx {
	return hereCtx{Where: locate.Where{Project: "demo", Root: f.repo, RepoDir: f.repo, Repo: "app"}}
}

// TestHereCloneJudgesTheMarkersTarget: a clone is judged against the target
// of the repo the marker names, and against main when it names none.
func TestHereCloneJudgesTheMarkersTarget(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\nrepos:\n  - name: app\n    target: develop\n")
	f.git(f.repo, "push", "-q", "origin", "main:develop")
	f.git(f.repo, "fetch", "-q", "origin")
	f.git(f.repo, "checkout", "-q", "-B", "develop", "origin/develop")
	f.originMoves(t, "develop", "d.txt")

	c := cloneCtx(f)
	if got := hereCloneTarget(c); got != "develop" {
		t.Fatalf("target = %q, want develop", got)
	}
	blocks, err := hereCloneBlocks(c)
	if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0], "of: origin/develop") || !strings.Contains(blocks[0], "pull --ff-only origin develop") {
		t.Fatalf("blocks = %q, err %v", blocks, err)
	}

	c.Where.Root = filepath.Join(ie.root, "nowhere")
	if got := hereCloneTarget(c); got != "main" {
		t.Errorf("target without a marker = %q, want main", got)
	}
	if blocks, _ := hereCloneBlocks(c); len(blocks) != 0 {
		t.Errorf("a clone on develop judged against main is named: %q", blocks)
	}
}

// TestHereCloneNamesADivergedCloneWithoutACommand: both counts and the
// sentence, in the block and in the json, and no pull.
func TestHereCloneNamesADivergedCloneWithoutACommand(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\n")
	writeTestFile(t, filepath.Join(f.repo, "local.txt"), "l\n")
	f.git(f.repo, "add", "-A")
	f.git(f.repo, "commit", "-q", "-m", "local")
	f.originMoves(t, "main", "x.txt")

	c := cloneCtx(f)
	blocks, err := hereCloneBlocks(c)
	if err != nil || len(blocks) != 1 {
		t.Fatalf("blocks = %q, err %v", blocks, err)
	}
	for _, want := range []string{"behind: 1", "ahead: 1", "of: origin/main", "a fast-forward pull will refuse"} {
		if !strings.Contains(blocks[0], want) {
			t.Errorf("block lacks %q:\n%s", want, blocks[0])
		}
	}
	if strings.Contains(blocks[0], "pull:") || strings.Contains(blocks[0], "pull --ff-only") {
		t.Errorf("a diverged clone is given a pull:\n%s", blocks[0])
	}
	r, ok := hereCloneJudged(c)
	info := hereCloneInfo(r)
	if !ok || info.Pull != "" || info.Ahead != 1 || info.Behind != 1 || info.Note != clonebehind.DivergedNote(f.repo, r) {
		t.Errorf("json = %+v ok %v", info, ok)
	}
}

// TestHereNamesTheCloneAsTheMachineMapSpellsIt: git reports a clone by its
// resolved path, while the map may hold a symlinked one; the pull command and
// the diverged note carry the map's, from the clone, the symlink and a linked
// worktree, and equal jig doctor's.
func TestHereNamesTheCloneAsTheMachineMapSpellsIt(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	f := newCloneFx(t, ie, "project: demo\nkind: project\n")
	link := filepath.Join(ie.root, "via-link")
	if err := os.Symlink(f.repo, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := project.UpdateMachine(ie.jigHome, func(m map[string]project.MachineProject) error {
		m["demo"] = project.MachineProject{Clones: map[string]string{"app": link}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(ie.root, "app.wt")
	f.git(f.repo, "worktree", "add", "-q", "-b", "feat", worktree)
	dirs := map[string]string{"the clone": f.repo, "the symlink": link, "a linked worktree": worktree}

	f.originMoves(t, "main", "x.txt")
	want := "git -C " + clonebehind.Quote(link) + " pull --ff-only origin main"
	for name, dir := range dirs {
		out, _ := runMain(t, ie.env.inDir(dir), "", "here")
		if !strings.Contains(out, want) {
			t.Errorf("here from %s:\n%s\nwant the map's spelling %q", name, out, want)
		}
		out, _ = runMain(t, ie.env.inDir(dir), "", "here", "--json")
		if !strings.Contains(out, strings.ReplaceAll(want, `\`, `\`)) && !strings.Contains(out, want) {
			t.Errorf("here --json from %s:\n%s\nwant the map's spelling %q", name, out, want)
		}
		out, _ = runMain(t, ie.env.inDir(dir), "", "doctor")
		if !strings.Contains(out, want) {
			t.Errorf("doctor from %s:\n%s\nwant %q", name, out, want)
		}
	}

	writeTestFile(t, filepath.Join(f.repo, "local.txt"), "l\n")
	f.git(f.repo, "add", "-A")
	f.git(f.repo, "commit", "-q", "-m", "local")
	for name, dir := range dirs {
		out, _ := runMain(t, ie.env.inDir(dir), "", "here")
		if !strings.Contains(out, "a fast-forward pull will refuse") || !strings.Contains(out, clonebehind.Quote(link)) {
			t.Errorf("diverged note from %s:\n%s\nwant the map's %q", name, out, link)
		}
	}
}
