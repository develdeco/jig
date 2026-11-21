package verifydeliver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// repoSpan is one repo's change as publish ships it: the clone that holds the
// head, the branch the pull request goes into, and the head.
type repoSpan struct {
	dir, base, head string
}

// lsTreeChunk bounds the paths one git call is given, so a change of
// thousands of files stays under the command line limit.
const lsTreeChunk = 200

// changeSize is how much code the changes in spans touched: the files each
// changed against its merge base with origin's base branch, and their bytes
// at that merge base, added up over the spans. A renamed file counts as a
// deleted one and an added one, and an added file has no bytes at the base.
func changeSize(spans []repoSpan) (journal.ChangeSize, error) {
	var total journal.ChangeSize
	for _, s := range spans {
		start, err := gitx.MergeBase(s.dir, "origin/"+s.base, s.head)
		if err != nil {
			return journal.ChangeSize{}, fmt.Errorf("change size: merge-base origin/%s %s: %w", s.base, s.head, err)
		}
		files, err := gitx.DiffNameOnly(s.dir, start, s.head, "ACDMRTUXB")
		if err != nil {
			return journal.ChangeSize{}, fmt.Errorf("change size: %w", err)
		}
		total.Files += len(files)
		for from := 0; from < len(files); from += lsTreeChunk {
			chunk := files[from:min(from+lsTreeChunk, len(files))]
			n, err := bytesAt(s.dir, start, chunk)
			if err != nil {
				return journal.ChangeSize{}, err
			}
			total.Bytes += n
		}
	}
	return total, nil
}

// bytesAt is the size of the files at rev in dir; a path rev does not have
// adds nothing.
func bytesAt(dir, rev string, paths []string) (int64, error) {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	out, err := gitx.Run(dir, append([]string{"ls-tree", "-r", "-l", "-z", rev, "--"}, paths...)...)
	if err != nil {
		return 0, fmt.Errorf("change size: %w", err)
	}
	var sum int64
	for _, entry := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		if !ok || !want[path] {
			continue
		}
		// <mode> <type> <object> <size>, the size right-aligned and "-" for
		// an entry with none (a submodule).
		f := strings.Fields(meta)
		if len(f) != 4 || f[1] != "blob" {
			continue
		}
		n, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("change size: size %q of %s: %w", f[3], path, err)
		}
		sum += n
	}
	return sum, nil
}

// readSpans are the spans of targets as the operator's clones hold them: each
// repo's reviewed head (else its branch) in its mapped clone.
func readSpans(d Deps, targets []*publishTarget) ([]repoSpan, error) {
	spans := make([]repoSpan, 0, len(targets))
	for _, pt := range targets {
		dir, err := readerDir(d, pt)
		if err != nil {
			return nil, err
		}
		spans = append(spans, repoSpan{dir: dir, base: pt.gitBase(dir), head: pt.readRev()})
	}
	return spans, nil
}

// ticketTargets are the targets of every repo the ticket publishes in, given
// those a publish with --repo worked in (targets): the publish that finishes a
// ticket may not be the one that shipped its other repos, and the change size
// is the whole ticket's. They are read before the publish writes anything.
func ticketTargets(d Deps, ticket, repoFlag string, targets []*publishTarget) ([]*publishTarget, error) {
	if repoFlag == "" || !targets[0].several() {
		return targets, nil
	}
	return readyToPublish(d, ticket, "", targets[0].into)
}

// publishDoneLine is the line a finished publish journals: with the change's
// size when it could be measured. A size that cannot be read is a warning and
// a line without one, never a publish that fails after its branch is out.
func publishDoneLine(spans []repoSpan, err error, warn func(string, ...any)) journal.Line {
	line := journal.Line{Event: journal.EventPublishDone}
	var size journal.ChangeSize
	if err == nil {
		size, err = changeSize(spans)
	}
	if err != nil {
		warn("jig: the size of the change was not recorded: %v\n", err)
		return line
	}
	line.Change = &size
	return line
}
