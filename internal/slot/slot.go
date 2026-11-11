// Package slot is the machine's suite slot: a whole-suite oracle of a project
// takes one before it starts, so two suites of one project do not fight for
// the same machine and both run past their time bound. A slot is a lock file
// under the jig home, taken with filelock, so the operating system frees it
// when its holder dies, however it dies, and nobody releases a dead holder's
// slot by hand.
package slot

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/home"
)

// pollInterval is the pause between rounds over the project's slots while
// every one is held.
const pollInterval = 100 * time.Millisecond

// Take takes the first free slot of 1..n for project, waiting and retrying
// while every one is held. onWait, when not nil, is called once, the first
// time a round finds none free, so the caller can say it waits. The wait has no
// bound of its own: the holder is a running suite with its own time limit.
// Cancelling ctx ends the wait with ctx's error. The returned release lets
// the slot go; it is safe to call more than once.
func Take(ctx context.Context, root, project string, n int, onWait func()) (release func(), err error) {
	if n < 1 {
		return nil, fmt.Errorf("slot: %d slots for project %q, want at least 1", n, project)
	}
	if project == "" || project != filepath.Base(project) || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return nil, fmt.Errorf("slot: %q is not a project name", project)
	}
	dir := home.SlotDir(root, project)
	waited := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i := 1; i <= n; i++ {
			rel, held, lerr := filelock.TryLock(filepath.Join(dir, strconv.Itoa(i)), 0)
			if lerr != nil {
				return nil, fmt.Errorf("slot: take %d of project %q: %w", i, project, lerr)
			}
			if held {
				return rel, nil
			}
		}
		if !waited {
			waited = true
			if onWait != nil {
				onWait()
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
