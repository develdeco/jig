// Package gatelock orders the gates of one ticket: `jig gate` takes the
// ticket's lock before it does anything that costs, so a second gate waits for
// the first instead of rerunning the suite beside it. The lock is a file under
// the jig home taken with filelock, so the operating system frees it when its
// holder dies, however it dies, and nobody releases a dead gate's lock by hand.
// A holder file beside it names who holds it, for whoever waits.
package gatelock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/home"
)

// pollInterval is the pause between tries while the lock is held.
const pollInterval = 100 * time.Millisecond

// Info names the gate that holds a ticket's lock.
type Info struct {
	PID   int
	Since time.Time
}

// Take takes the lock of ticket in project, waiting while another gate holds
// it. onWait, when not nil, is called once, the first time the lock is found
// held, so the caller can say it waits. The wait has no bound of its own: the
// holder is a gate with its own limits. Cancelling ctx ends the wait with
// ctx's error. The returned release lets the lock go; it is safe to call more
// than once.
func Take(ctx context.Context, root, project, ticket string, onWait func()) (release func(), err error) {
	if !plainName(project) || !plainName(ticket) {
		return nil, fmt.Errorf("gatelock: %q/%q is not a project and ticket name", project, ticket)
	}
	path := filepath.Join(home.GateDir(root, project), ticket)
	waited := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, held, lerr := filelock.TryLock(path, 0)
		if lerr != nil {
			return nil, fmt.Errorf("gatelock: take %s of project %q: %w", ticket, project, lerr)
		}
		if held {
			body := fmt.Sprintf("%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = os.WriteFile(path+".holder", []byte(body), 0o644)
			released := false
			return func() {
				if released {
					return
				}
				released = true
				_ = os.Remove(path + ".holder")
				rel()
			}, nil
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

// Holder reads the holder file of ticket's lock. ok is false when there is
// none or it cannot be read, as when the holder has the lock but has not
// written it yet.
func Holder(root, project, ticket string) (Info, bool) {
	if !plainName(project) || !plainName(ticket) {
		return Info{}, false
	}
	b, err := os.ReadFile(filepath.Join(home.GateDir(root, project), ticket+".holder"))
	if err != nil {
		return Info{}, false
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		return Info{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Info{}, false
	}
	since, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return Info{}, false
	}
	return Info{PID: pid, Since: since}, true
}

// plainName is whether s is one path element, so a name cannot leave the gate
// directory.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && s == filepath.Base(s) && !strings.ContainsAny(s, `/\`)
}
