// Package clonebehind judges whether a project's clone on this machine is
// behind its origin's target branch. Both jig here and jig doctor call Check,
// so they cannot disagree. It never fetches and never runs git status: the
// judgment is a local count against refs/remotes/origin/<target> as of the
// clone's last fetch, so it is safe at every session start (ADR 0034: jig
// advises, the person or session runs the pull).
package clonebehind

import (
	"fmt"
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
	// NoRef is set when the clone has the target checked out and an origin
	// remote but no remote-tracking ref for the target.
	NoRef bool
	// NoRemote is set when the clone has the target checked out but no
	// origin remote at all.
	NoRemote bool
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
		if _, err := gitx.Run(dir, "remote", "get-url", "origin"); err != nil {
			r.NoRemote = true
			r.Reason = "the clone has no origin remote"
			return r
		}
		r.NoRef = true
		r.Reason = "the clone has no " + of
		return r
	}
	behind, ahead, err := count(dir, ref)
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
		r.Pull = "git -C " + Quote(dir) + " pull --ff-only origin " + target
	default:
		r.State = Current
	}
	return r
}

// count returns how many commits ref has that HEAD lacks and how many HEAD
// has that ref lacks, in one git call.
func count(dir, ref string) (behind, ahead int, err error) {
	out, err := gitx.Run(dir, "rev-list", "--left-right", "--count", ref+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("rev-list --count printed %q", out)
	}
	if behind, err = strconv.Atoi(f[0]); err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(f[1])
	return behind, ahead, err
}

// Quote spells a path for a shell command line: as it is when it is plain,
// else in double quotes.
func Quote(path string) string {
	if path != "" && !strings.ContainsAny(path, " 	\\\"'$`&;()<>|*?[]{}!#~") {
		return path
	}
	return `"` + strings.NewReplacer(`"`, `\"`, "$", `\$`, "`", "\\`").Replace(path) + `"`
}

// DivergedNote is the sentence for a diverged clone: both counts, and that a
// fast-forward pull would refuse. It names no command that rewrites work.
func DivergedNote(dir string, r Result) string {
	return "clone " + dir + " has diverged from " + r.Of + " (as of its last fetch): " +
		strconv.Itoa(r.Behind) + " behind, " + strconv.Itoa(r.Ahead) +
		" ahead; a fast-forward pull will refuse, so it needs a merge or rebase by a person"
}
