package correct

import (
	"fmt"
	"path"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// sliceTrailer is the commit trailer that names the slice a commit is for.
const sliceTrailer = "Jig-Slice"

// A SliceGuess is a commit that names no slice of the ticket, with the slices
// the files it changed point to. It is an inference and nothing more: jig
// verifies nothing for the commit, and no slice is green because of it.
type SliceGuess struct {
	Commit string
	// Trailer is the slice the commit's Jig-Slice trailer names when the
	// ticket has no such slice, "" when the commit carries no trailer.
	Trailer string
	// Files are the files the commit changed, as git prints them.
	Files []string
	// Owners are the slices whose workspace holds a changed file, in the
	// order of the ticket's slices: one names the slice, several are the
	// candidates, none says no workspace holds the commit.
	Owners []SliceWorkspace
	// Paths are the workspace paths the slices build in.
	Paths []string
}

// Ref fingerprints the guess by its commit.
func (g SliceGuess) Ref() string { return "slice:" + short12(g.Commit) }

// Owners returns the slices of ws that own a file of files, in the order of
// ws. A file belongs to the slices whose workspace is the deepest to hold it,
// so that a slice that builds in the whole repo does not claim the files of a
// workspace inside it; slices that share the workspace share the file. A path
// of "." is the whole repo, and the shallowest workspace of all.
func Owners(files []string, ws []SliceWorkspace) []SliceWorkspace {
	owned := make([]bool, len(ws))
	for _, f := range files {
		deepest := -1
		for _, w := range ws {
			if within(f, []string{w.Path}) {
				deepest = max(deepest, depth(w.Path))
			}
		}
		for i, w := range ws {
			if deepest >= 0 && depth(w.Path) == deepest && within(f, []string{w.Path}) {
				owned[i] = true
			}
		}
	}
	var out []SliceWorkspace
	for i, w := range ws {
		if owned[i] {
			out = append(out, w)
		}
	}
	return out
}

// depth is how many directories a cleaned workspace path goes down; the whole
// repo, ".", is none.
func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// Unsliced looks at commit sha in the repository around dir, the repo called
// repo of the project, and reports whether it names no slice of the ticket: it
// has no Jig-Slice trailer, or the trailer names a slice the ticket does not
// have. If so it says which slices the files the commit changed belong to,
// against the workspaces as that repository's manifest places them. It reports
// false for a commit that is not slice work to infer from: one that names a
// slice, a merge, one with no parent to compare with or no file, and one in a
// repo none of the slices build in.
func Unsliced(dir, repo, sha string, all []store.Slice) (SliceGuess, bool, error) {
	top, err := gitx.TopLevel(dir)
	if err != nil {
		return SliceGuess{}, false, err
	}
	trailer, err := gitx.Trailer(top, sha, sliceTrailer)
	if err != nil {
		return SliceGuess{}, false, err
	}
	if hasSlice(all, trailer) {
		return SliceGuess{}, false, nil
	}
	out, err := gitx.Run(top, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return SliceGuess{}, false, err
	}
	// The line is the commit and then its parents.
	if len(strings.Fields(out)) != 2 {
		return SliceGuess{}, false, nil
	}
	m, err := manifest.Resolve(top)
	if err != nil {
		return SliceGuess{}, false, err
	}
	ws := SliceWorkspaces(m, repo, all)
	if len(ws) == 0 {
		return SliceGuess{}, false, nil
	}
	changed, err := gitx.DiffNameOnly(top, sha+"^", sha, "ADMT")
	if err != nil {
		return SliceGuess{}, false, err
	}
	if len(changed) == 0 {
		return SliceGuess{}, false, nil
	}
	return SliceGuess{Commit: sha, Trailer: trailer, Files: changed, Owners: Owners(changed, ws), Paths: WorkspacePaths(m, repo, all)}, true, nil
}

// hasSlice reports whether the ticket has a slice called id.
func hasSlice(all []store.Slice, id string) bool {
	if id == "" {
		return false
	}
	for _, sl := range all {
		if sl.ID == id {
			return true
		}
	}
	return false
}

// sliceFact finds the commit the event made when it names no slice, if any.
// A session that has claimed a slice names it for every commit it makes (the
// claim is what jig verifies a commit with no trailer for), so it has none.
func sliceFact(in Input, all []store.Slice) (fact, error) {
	if in.Cwd == "" || hasSlice(all, in.Slice) {
		return fact{}, errNone
	}
	g, unsliced, err := Unsliced(in.Cwd, in.Repo, in.Commit, all)
	if err != nil {
		return fact{}, err
	}
	if !unsliced {
		return fact{}, errNone
	}
	return fact{
		note:   Note{Ref: g.Ref(), Kind: KindSlice, Text: sliceText(in.Ticket, g)},
		commit: g.Commit, files: g.Files,
	}, nil
}

// stillUnsliced reports whether the commit of an earlier slice-missing note is
// still as the note said: in the history of HEAD, not amended away, and with
// no verification of it in the journal, which is what saying its slice with
// `jig verify` leaves. Anything git cannot show is not still true.
func stillUnsliced(in Input, sha string, lines []journal.Line) bool {
	if sha == "" || in.Cwd == "" {
		return false
	}
	for _, l := range lines {
		switch l.Event {
		case journal.EventVerifyPending, journal.EventOracle, journal.EventVerified:
			if l.Commit == sha {
				return false
			}
		}
	}
	top, err := gitx.TopLevel(in.Cwd)
	if err != nil {
		return false
	}
	held, err := gitx.IsAncestor(top, sha, "HEAD")
	return err == nil && held
}

// sliceText is what the session is told of a commit that names no slice.
func sliceText(ticket string, g SliceGuess) string {
	sha := short12(g.Commit)
	var b strings.Builder
	if g.Trailer != "" {
		fmt.Fprintf(&b, "commit %s on %s carries `%s: %s`, but %s has no slice %s, so jig verifies nothing for it.", sha, ticket, sliceTrailer, g.Trailer, ticket, g.Trailer)
	} else {
		fmt.Fprintf(&b, "commit %s on %s names no slice, so jig verifies nothing for it.", sha, ticket)
	}
	amend := sliceTrailer + ": <slice>"
	switch len(g.Owners) {
	case 0:
		fmt.Fprintf(&b, " It touched no slice's workspace (%s): if it is work for a slice, ", listed(asDirs(g.Paths), 5))
	case 1:
		o := g.Owners[0]
		fmt.Fprintf(&b, " It touched %s (slice %s's workspace): ", asDir(o.Path), o.Slice)
		amend = sliceTrailer + ": " + o.Slice
	default:
		names := make([]string, len(g.Owners))
		for i, o := range g.Owners {
			names[i] = fmt.Sprintf("%s (%s)", o.Slice, asDir(o.Path))
		}
		fmt.Fprintf(&b, " It touched the workspaces of slices %s: ", listed(names, 5))
	}
	fmt.Fprintf(&b, "amend it with `%s`, or say which slice it is with `jig verify %s %s --slice <slice>`.", amend, ticket, sha)
	return b.String()
}

// asDir is a workspace path as a directory.
func asDir(p string) string {
	if p == "." {
		return "./"
	}
	return path.Clean(p) + "/"
}

// asDirs is each of paths as a directory.
func asDirs(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = asDir(p)
	}
	return out
}
