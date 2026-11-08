package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// deltaRepo makes a real git repo holding base, then commits change on top and
// returns the repo and the two commits: the delta a gate round reads.
func deltaRepo(t *testing.T, base, change map[string]string) (dir, baseSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	gitMust(t, dir, "init", "-q")
	write := func(files map[string]string) {
		for name, body := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gitMust(t, dir, "add", "-A")
		gitMust(t, dir, "commit", "-q", "-m", "c")
	}
	write(base)
	baseSHA = gitMust(t, dir, "rev-parse", "HEAD")
	write(change)
	return dir, baseSHA, gitMust(t, dir, "rev-parse", "HEAD")
}

// realDeltaContext runs the code a delta round's context runs - the diff and
// its files from git (computeDeltaDiffs), then the render (roundBlocks) - over
// repos, with the round's hunks read from git as the gate reads them, named
// with the repo as the round names them. qualified is false for a single repo.
func realDeltaContext(t *testing.T, repos []verifydeliver.PreparedRepo, qualified bool) (string, []deltaDiff) {
	t.Helper()
	p := deltaPrepared(verifydeliver.IntentSourceBrief)
	p.RequestPath = filepath.Join(t.TempDir(), "review.json")
	p.Request.Hunks = nil
	if qualified {
		p.Repos = repos
	} else {
		p.Lease, p.Base, p.Head = repos[0].Lease, repos[0].Base, repos[0].Head
	}
	for _, r := range repos {
		hunks, err := gitx.DiffHunks(r.Lease, r.Base, r.Head)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hunks {
			name := h.Path
			if qualified {
				name = r.Repo + "/" + name
			}
			p.Request.Hunks = append(p.Request.Hunks, verifydeliver.HunkEntry{File: name, From: h.From, To: h.To})
		}
	}
	diffs, err := computeDeltaDiffs(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(roundBlocks(p, nil, true, diffs), "\n"), diffs
}

// TestASingleRepoDeltaListsItsModifiedFilesHunksThroughGit: in a single repo
// the delta's files carry no repo prefix, so the unprobed work list holds
// exactly the modified file's hunk.
func TestASingleRepoDeltaListsItsModifiedFilesHunksThroughGit(t *testing.T) {
	t.Parallel()
	dir, base, head := deltaRepo(t,
		map[string]string{"beta/beta.go": "package beta\n", "beta/other.go": "package beta\n"},
		map[string]string{"beta/beta.go": "package beta\n// changed\n"})
	out, _ := realDeltaContext(t, []verifydeliver.PreparedRepo{{Lease: dir, Base: base, Head: head, Scope: "delta"}}, false)
	if want := "unprobed[1]{hunk}:\n  \"beta/beta.go:2-2\"\n"; !strings.Contains(out+"\n", want) {
		t.Errorf("the work list is not exactly beta/beta.go:2-2\n%s", out)
	}
}

// TestADeltaThatAddsAFileListsItsHunksThroughGit: a file the delta adds is in
// the delta's files, so its hunk is in the work list beside the modified one.
func TestADeltaThatAddsAFileListsItsHunksThroughGit(t *testing.T) {
	t.Parallel()
	dir, base, head := deltaRepo(t,
		map[string]string{"beta/beta.go": "package beta\n"},
		map[string]string{"beta/beta.go": "package beta\n// changed\n", "beta/new.go": "package beta\n// new\n"})
	out, diffs := realDeltaContext(t, []verifydeliver.PreparedRepo{{Lease: dir, Base: base, Head: head, Scope: "delta"}}, false)
	if len(diffs) != 1 || strings.Join(diffs[0].Files, ",") != "beta/beta.go,beta/new.go" {
		t.Errorf("delta files = %+v, want beta/beta.go and beta/new.go", diffs)
	}
	if want := "unprobed[2]{hunk}:\n  \"beta/beta.go:2-2\"\n  \"beta/new.go:1-2\"\n"; !strings.Contains(out+"\n", want) {
		t.Errorf("the work list is not exactly the modified and the added file's hunks\n%s", out)
	}
}

// TestAMultiRepoDeltaLabelsEachRepoAndNamesItsLongDiffFile: each repo's block
// opens with its repo label, and a repo whose diff is over the line bound has
// its own file, named for it, beside the request.
func TestAMultiRepoDeltaLabelsEachRepoAndNamesItsLongDiffFile(t *testing.T) {
	t.Parallel()
	var long strings.Builder
	long.WriteString("package web\n")
	for i := 0; i < deltaMaxLines+10; i++ {
		fmt.Fprintf(&long, "// line %d\n", i)
	}
	apiDir, apiBase, apiHead := deltaRepo(t, map[string]string{"a.go": "package api\n"}, map[string]string{"a.go": "package api\n// x\n"})
	webDir, webBase, webHead := deltaRepo(t, map[string]string{"w.go": "package web\n"}, map[string]string{"w.go": long.String()})
	out, diffs := realDeltaContext(t, []verifydeliver.PreparedRepo{
		{Repo: "api", Lease: apiDir, Base: apiBase, Head: apiHead, Scope: "delta"},
		{Repo: "web", Lease: webDir, Base: webBase, Head: webHead, Scope: "delta"},
	}, true)
	if !strings.Contains(out, "delta:\n  repo: api\n") || !strings.Contains(out, "  repo: web\n") {
		t.Errorf("the delta block lacks a repo label per repo\n%s", out)
	}
	if diffs[0].Path != "" || filepath.Base(diffs[1].Path) != "delta-web.diff" {
		t.Fatalf("diff paths = %q and %q, want api's printed and web's in delta-web.diff", diffs[0].Path, diffs[1].Path)
	}
	if !strings.Contains(out, "  diff_path: \""+diffs[1].Path+"\"\n") || !strings.Contains(out, "  diff: |\n    diff --git a/a.go b/a.go") {
		t.Errorf("the block does not print api's diff and name web's file\n%s", out)
	}
	if _, err := os.Stat(diffs[1].Path); err != nil {
		t.Errorf("web's diff file: %v", err)
	}
}
