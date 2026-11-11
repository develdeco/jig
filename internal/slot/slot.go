// Package slot is the machine's suite slot: a whole-suite oracle of a project
// takes one before it starts, so two suites of one project do not fight for
// the same machine and both run past their time bound. A slot is a lock file
// under the jig home, taken with filelock, so the operating system frees it
// when its holder dies, however it dies, and nobody releases a dead holder's
// slot by hand. A run that waits holds a lock of its own on a waiting file for
// as long as it waits, which is how a reader tells a live waiter from one
// that died.
package slot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// pollInterval is the pause between rounds over the project's slots while
// every one is held.
const pollInterval = 100 * time.Millisecond

// waitingDir is the directory under a project's slot directory where the runs
// that wait keep their waiting files.
const waitingDir = "waiting"

// Who names a run for the line that says it holds or awaits a slot:
// "<ticket>/<slice>" for a slice's verification, "<ticket>/gate" for a gate.
func Who(ticket, slice string) string {
	if slice == "" {
		slice = "gate"
	}
	return ticket + "/" + slice
}

// Take takes the first free slot of 1..n for project, waiting and retrying
// while every one is held. who names the run (Who), for those who read the
// slots. onWait, when not nil, is called once, the first time a round finds
// none free, so the caller can say it waits. The wait has no bound of its own:
// the holder is a running suite with its own time limit. Cancelling ctx ends
// the wait with ctx's error. The returned release lets the slot go; it is safe
// to call more than once.
func Take(ctx context.Context, root, project string, n int, who string, onWait func()) (release func(), err error) {
	if n < 1 {
		return nil, fmt.Errorf("slot: %d slots for project %q, want at least 1", n, project)
	}
	if project == "" || project != filepath.Base(project) || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return nil, fmt.Errorf("slot: %q is not a project name", project)
	}
	dir := home.SlotDir(root, project)
	var endWait func()
	defer func() {
		if endWait != nil {
			endWait()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i := 1; i <= n; i++ {
			path := filepath.Join(dir, strconv.Itoa(i))
			rel, held, lerr := filelock.TryLock(path, 0)
			if lerr != nil {
				return nil, fmt.Errorf("slot: take %d of project %q: %w", i, project, lerr)
			}
			if held {
				_ = os.WriteFile(path+".holder", []byte(who), 0o644)
				return func() {
					_ = os.Remove(path + ".holder")
					rel()
				}, nil
			}
		}
		if endWait == nil {
			endWait = startWaiting(dir, who)
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

var waiterSeq atomic.Int64

// startWaiting registers a waiting run: a waiting file naming who, locked for
// as long as the run waits. The returned function ends the wait.
func startWaiting(dir, who string) func() {
	id := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), waiterSeq.Add(1))
	path := filepath.Join(dir, waitingDir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	_ = os.WriteFile(path+".who", []byte(who), 0o644)
	rel, held, err := filelock.TryLock(path, 0)
	if err != nil || !held {
		_ = os.Remove(path + ".who")
		return func() {}
	}
	return func() {
		rel()
		_ = os.Remove(path + ".lock")
		_ = os.Remove(path + ".who")
	}
}

// Hold is Take for an oracle run: it records journal.EventSlotWait when the
// run has to wait and journal.EventSlotTaken when it gets the slot, through
// record, which writes the line for the event it is handed, with the summary
// a wait carries (Line). A record that
// fails gives the slot back and returns its error.
func Hold(ctx context.Context, root, project string, n int, who string, record func(event, summary string) error) (release func(), err error) {
	var werr error
	rel, err := Take(ctx, root, project, n, who, func() {
		summary := ""
		if st, rerr := Read(root, project); rerr == nil {
			summary = Line(st)
		}
		werr = record(journal.EventSlotWait, summary)
	})
	if err != nil {
		return nil, err
	}
	if werr == nil {
		werr = record(journal.EventSlotTaken, "")
	}
	if werr != nil {
		rel()
		return nil, werr
	}
	return rel, nil
}

// Status is who holds the project's slots and who waits for one right now.
type Status struct {
	// Holders are the Who of each slot in use ("" when its holder left no
	// name); Waiters those of the live runs waiting for a slot.
	Holders []string
	Waiters []string
}

// Read reads the project's slots as they are (every slot that was ever taken): a slot is in use when its
// lock is held, and a waiter is live when its waiting file is locked, so a
// holder or waiter that died is not counted. It takes and gives back each
// lock it tests, which a taker at that moment sees as a slot still held and
// asks again.
func Read(root, project string) (Status, error) {
	var s Status
	dir := home.SlotDir(root, project)
	slots, err := filepath.Glob(filepath.Join(dir, "[0-9]*.lock"))
	if err != nil {
		return s, err
	}
	sort.Strings(slots)
	for _, lock := range slots {
		path := strings.TrimSuffix(lock, ".lock")
		rel, held, err := filelock.TryLock(path, 0)
		if err != nil {
			return s, err
		}
		if held {
			rel()
			continue
		}
		who, _ := os.ReadFile(path + ".holder")
		s.Holders = append(s.Holders, strings.TrimSpace(string(who)))
	}
	entries, err := os.ReadDir(filepath.Join(dir, waitingDir))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".lock")
		if !ok {
			continue
		}
		path := filepath.Join(dir, waitingDir, id)
		rel, held, err := filelock.TryLock(path, 0)
		if err != nil {
			return s, err
		}
		if held {
			// Its waiter died: the OS let the lock go.
			rel()
			_ = os.Remove(path + ".lock")
			_ = os.Remove(path + ".who")
			continue
		}
		who, _ := os.ReadFile(path + ".who")
		s.Waiters = append(s.Waiters, strings.TrimSpace(string(who)))
	}
	return s, nil
}

// Line is the one line that says a run waits: who is running its suite and how
// many wait, or just that it waits when no holder is known.
func Line(s Status) string {
	for _, h := range s.Holders {
		if h != "" {
			return fmt.Sprintf("waiting for the suite slot: %s is running its suite (%d waiting)", h, max(len(s.Waiters), 1))
		}
	}
	return "waiting for the suite slot"
}

// Wait is one run of a ticket that waits for the suite slot now: the slice it
// verifies ("" for the ticket's gate round) and the line that says so.
type Wait struct {
	Slice string
	Line  string
}

// Waits are the runs of ticket that wait for a slot of project now. The
// ticket's journal lines say which runs began to wait and were not given the
// slot since; the slots say which of those still wait, since a run that died
// waiting leaves its slot-wait line standing, and who holds the slot and how
// many wait.
func Waits(root, project, ticket string, lines []journal.Line) ([]Wait, error) {
	open := map[string]bool{}
	var order []string
	for _, l := range lines {
		switch l.Event {
		case journal.EventSlotWait:
			if !open[l.Slice] {
				order = append(order, l.Slice)
			}
			open[l.Slice] = true
		case journal.EventSlotTaken:
			open[l.Slice] = false
		}
	}
	var out []Wait
	var st Status
	read := false
	for _, slice := range order {
		if !open[slice] {
			continue
		}
		if !read {
			var err error
			if st, err = Read(root, project); err != nil {
				return nil, err
			}
			read = true
		}
		who := Who(ticket, slice)
		live := false
		for _, w := range st.Waiters {
			live = live || w == who
		}
		if live {
			out = append(out, Wait{Slice: slice, Line: Line(st)})
		}
	}
	return out, nil
}
