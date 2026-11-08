// Package clonebehind judges whether a project's clone on this machine is
// behind its origin's target branch. Both jig here and jig doctor call Check,
// so they cannot disagree. It never fetches and never runs git status: the
// judgment is a local count against refs/remotes/origin/<target> as of the
// clone's last fetch, so it is safe at every session start (ADR 0034: jig
// advises, the person or session runs the pull).
package clonebehind

import (
	"os"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
)

// State is what Check decided.
type State string

const (
	// Current: the clone is level with or ahead of origin's target, or is
	// deliberately elsewhere; there is nothing to say.
	Current State = "current"
	// Behind: origin's target has commits the clone lacks and the clone has
	// none origin lacks; a fast-forward pull applies.
	Behind State = "behind"
	// Diverged: both sides have commits the other lacks; a fast-forward
	// pull would refuse.
	Diverged State = "diverged"
	// Unjudged: the clone cannot be judged (see Reason).
	Unjudged State = "unjudged"
)

// Result is one clone's judgment.
type Result struct {
	State State
	// Behind and Ahead are the commit counts (HEAD..origin/<target> and
	// origin/<target>..HEAD); set for Behind and Diverged.
	Behind int
	Ahead  int
	// Dir is the clone judged.
	Dir string
	// Of names the ref judged against, "origin/<target>".
	Of string
	// Pull is the command that brings a Behind clone up to date.
	Pull string
	// NoRef is set when the clone has the target checked out but no
	// remote-tracking ref for it.
	NoRef bool
	// Reason says why an Unjudged clone was not judged.
	Reason string
}

// Check judges the clone in dir against origin's target branch. It reads
// only local state.
func Check(dir, target string) Result {
	of := "origin/" + target
	r := Result{State: Unjudged, Dir: dir, Of: of}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		r.Reason = "the clone is not on disk"
		return r
	}
	if _, err := gitx.TopLevel(dir); err != nil {
		r.Reason = "the folder is not a git repository"
		return r
	}
	branch, ok := gitx.HeadBranch(dir)
	if !ok || branch != target {
		r.Reason = "the clone does not have " + target + " checked out"
		return r
	}
	ref := "refs/remotes/origin/" + target
	exists, err := gitx.RefExists(dir, ref)
	if err != nil {
		r.Reason = "the clone's refs could not be read"
		return r
	}
	if !exists {
		r.NoRef = true
		r.Reason = "the clone has no " + of
		return r
	}
	behind, err := count(dir, "HEAD.."+ref)
	if err != nil {
		r.Reason = "the commits could not be counted"
		return r
	}
	ahead, err := count(dir, ref+"..HEAD")
	if err != nil {
		r.Reason = "the commits could not be counted"
		return r
	}
	r.Behind, r.Ahead = behind, ahead
	switch {
	case behind > 0 && ahead > 0:
		r.State = Diverged
	case behind > 0:
		r.State = Behind
		r.Pull = "git -C " + dir + " pull --ff-only"
	default:
		r.State = Current
	}
	return r
}

func count(dir, rangeSpec string) (int, error) {
	out, err := gitx.Run(dir, "rev-list", "--count", rangeSpec)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// DivergedNote is the sentence for a diverged clone: both counts, and that a
// fast-forward pull would refuse. It names no command that rewrites work.
func DivergedNote(dir string, r Result) string {
	return "clone " + dir + " has diverged from " + r.Of + " (as of its last fetch): " +
		strconv.Itoa(r.Behind) + " behind, " + strconv.Itoa(r.Ahead) +
		" ahead; a fast-forward pull will refuse, so it needs a merge or rebase by a person"
}
