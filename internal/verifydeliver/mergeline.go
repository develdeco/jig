package verifydeliver

import (
	"errors"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// lineKind says how a head relates to the head a gate round reviewed (ADR 0038):
// the head is reviewed through merges when every commit between them, along the
// first parents, is a merge whose tree is git's own merge of its two parents.
type lineKind int

const (
	// lineOther: anything else - an ordinary commit, an octopus, a merge git
	// would not have made, a line that does not start at the reviewed head. The
	// head is not reviewed, and a delta round reads everything since.
	lineOther lineKind = iota
	// lineSame: the head is the reviewed head.
	lineSame
	// lineClean: every commit of the line is a two-parent merge whose tree
	// equals git's own merge of its parents. The merge brought in what the target
	// has, and added nothing of its own: there is nothing to review in it.
	lineClean
	// lineResolved: merges only, every one clean but the tip, which a person
	// resolved by hand. Its ownTree is git's own merge of the tip's parents; the
	// hand edits are the tip's tree against it.
	lineResolved
)

// classifyLine says how head relates to reviewed in the repository dir. A merge
// counts only when it brings in the target: its second parent is
// reachable from targetRef (origin/<target>, or the branch of the parent a
// stacked ticket goes into); a merge of any other branch is lineOther, and the
// reviewer reads what it brought. ownTree
// is, for lineResolved, git's own merge of the tip's parents. A git failure (an
// object dir lacks, say) or an empty dir is lineOther with no error, so a refusal
// stays what it was; only a git too old to merge without a work tree
// (gitx.CodeGitTooOld) is an error. The log alone says whether every commit of
// the line is a merge, so a line with an ordinary commit in it is lineOther
// before git is asked to merge anything: only a line that could be reviewed
// through merges reaches git's merge, and a git too old to make it is an error
// for that line alone.
func classifyLine(dir, reviewed, head, targetRef string) (kind lineKind, ownTree string, err error) {
	if reviewed != "" && head == reviewed {
		return lineSame, "", nil
	}
	if dir == "" || reviewed == "" || head == "" {
		return lineOther, "", nil
	}
	line, lerr := gitx.FirstParentLine(dir, reviewed, head)
	if lerr != nil || len(line) == 0 || len(line[0].Parents) == 0 || line[0].Parents[0] != reviewed {
		return lineOther, "", nil
	}
	for _, c := range line {
		if len(c.Parents) != 2 {
			return lineOther, "", nil
		}
		if targetRef == "" {
			return lineOther, "", nil
		}
		if in, aerr := gitx.IsAncestor(dir, c.Parents[1], targetRef); aerr != nil || !in {
			return lineOther, "", nil
		}
	}
	for i, c := range line {
		tree, clean, merr := gitx.MergeTree(dir, c.Parents[0], c.Parents[1])
		if merr != nil {
			if tooOld(merr) {
				return lineOther, "", merr
			}
			return lineOther, "", nil
		}
		switch {
		case tree == c.Tree && clean:
			// git's own merge.
		case i == len(line)-1 && tree != c.Tree:
			// The tip, resolved by hand (or a conflict resolved).
			return lineResolved, tree, nil
		default:
			// A hand-resolved merge that is not the tip, or a conflict committed
			// with its markers: the reviewer reads it all.
			return lineOther, "", nil
		}
	}
	return lineClean, "", nil
}

// tooOld says err is the one error classifyLine passes on: git cannot make a
// merge without a work tree.
func tooOld(err error) bool {
	var axiErr *axi.Error
	return errors.As(err, &axiErr) && axiErr.Code == gitx.CodeGitTooOld
}

// headReviewed says head is reviewed through the head a gate round reviewed:
// it is that head, or only merges of targetRef git itself would have made lie
// between them.
func headReviewed(dir, reviewed, head, targetRef string) (bool, error) {
	kind, _, err := classifyLine(dir, reviewed, head, targetRef)
	if err != nil {
		return false, err
	}
	return kind == lineSame || kind == lineClean, nil
}
