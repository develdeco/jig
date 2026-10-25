package correct

import (
	"fmt"
	"path"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// A ScopeDrift is a commit that changed files outside every workspace the
// ticket's slices build in.
type ScopeDrift struct {
	Commit string
	// Files are the changed files outside those workspaces, as git prints
	// them (repo-relative, forward slashes).
	Files []string
	// Paths are the workspace paths the slices build in.
	Paths []string
}

// Ref fingerprints the drift by its commit.
func (d ScopeDrift) Ref() string { return "scope:" + short12(d.Commit) }

// WorkspacePaths lists the paths, cleaned and with forward slashes, of the
// workspaces the slices build in, as the manifest m of the repo called repo
// places them (a slice of a project of several repos writes its workspace as
// "<repo>/<id>"). A slice whose workspace m does not have builds in another
// repo of the project, so it says nothing about this one and is passed over.
func WorkspacePaths(m manifest.Manifest, repo string, all []store.Slice) []string {
	var out []string
	seen := map[string]bool{}
	for _, sl := range all {
		ws, ok := m.WorkspaceIn(repo, sl.Workspace)
		if !ok {
			continue
		}
		p := path.Clean(strings.ReplaceAll(ws.Path, "\\", "/"))
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// Outside returns the files that are under none of paths, in their order. A
// path of "." is the whole repo. With no paths there is no scope to judge
// against, and no file is outside it.
func Outside(files, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if !within(f, paths) {
			out = append(out, f)
		}
	}
	return out
}

// within reports whether file is at or under one of paths.
func within(file string, paths []string) bool {
	file = path.Clean(file)
	for _, p := range paths {
		if p == "." || file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// Scope looks at what commit sha changed in the repository around dir, the
// repo called repo of the project, against the workspaces the slices build in,
// as that repository's manifest places them. It reports false when nothing is
// outside them, and when there is nothing to judge by: the slices build in no
// workspace this manifest has, or the commit has no parent to compare with.
func Scope(dir, repo, sha string, all []store.Slice) (ScopeDrift, bool, error) {
	top, err := gitx.TopLevel(dir)
	if err != nil {
		return ScopeDrift{}, false, err
	}
	m, err := manifest.Resolve(top)
	if err != nil {
		return ScopeDrift{}, false, err
	}
	paths := WorkspacePaths(m, repo, all)
	if len(paths) == 0 {
		return ScopeDrift{}, false, nil
	}
	if _, err := gitx.RevParse(top, sha+"^"); err != nil {
		return ScopeDrift{}, false, nil
	}
	changed, err := gitx.DiffNameOnly(top, sha+"^", sha, "ADMT")
	if err != nil {
		return ScopeDrift{}, false, err
	}
	outside := Outside(changed, paths)
	if len(outside) == 0 {
		return ScopeDrift{}, false, nil
	}
	return ScopeDrift{Commit: sha, Files: outside, Paths: paths}, true, nil
}

// scopeText is what the session is told of a drift.
func scopeText(ticket string, d ScopeDrift) string {
	return fmt.Sprintf("commit %s changed %d %s outside every workspace the slices of %s build in (%s): %s. Keep a commit to those workspaces.",
		short12(d.Commit), len(d.Files), plural(len(d.Files), "file", "files"), ticket, listed(d.Paths, 5), listed(d.Files, 5))
}
