// Package slot is the machine's budget for heavy oracle runs: a whole-suite
// oracle of any project asks for its share before it starts, waits in arrival
// order, holds the share until it ends and gives it back, so heavy runs do not
// fight for one machine and run past their time bound.
//
// The budget is a queue under the jig home, shared by every project on the
// machine. A run takes the next arrival number and registers an entry whose
// lock file it holds for as long as it lives, waiting or holding; the
// operating system frees that lock when the run dies, however it dies, which
// is how a reader tells a live entry from a dead one, and nobody cleans up
// after a dead run. Admission is decided under a short lock on the queue: a
// run is admitted when it is the oldest live run waiting on the budget and
// what it needs fits. Nothing passes it, so a small run never starves a
// large one; every waiter polls on its own clock, but only the oldest can be
// admitted, so the poll decides when, never who.
//
// A run of a project whose marker pins a count (Request.Pinned) is the
// person's own number: it waits for its project's count, not for the budget,
// and does not hold up the runs that wait on the budget.
package slot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// pollInterval is the pause between asks while a run waits for its turn.
const pollInterval = 100 * time.Millisecond

// queueLockWait is how long a run waits for the short lock on the queue
// before it gives up: the lock is held only for one scan of the entries.
const queueLockWait = 10 * time.Second

// entriesDir is the directory under the queue where each run keeps its entry.
const entriesDir = "entries"

// Who names a run for the line that says it holds or awaits a slot:
// "<ticket>/<slice>" for a slice's verification, "<ticket>/gate" for a gate.
func Who(ticket, slice string) string {
	if slice == "" {
		slice = "gate"
	}
	return ticket + "/" + slice
}

// Request is what a heavy oracle run asks the machine for: whose it is, which
// oracle and command it runs, and the count the project's marker pinned.
type Request struct {
	Project string
	// Who is the run's name (Who).
	Who string
	// Oracle and Command are the oracle's name and the command it runs.
	Oracle, Command string
	// Pinned is the count the marker pinned, 0 when it pinned none.
	Pinned int
}

// entryInfo is what an entry file records of its run.
type entryInfo struct {
	Project string `json:"project"`
	Who     string `json:"who"`
	Pinned  int    `json:"pinned,omitempty"`
}

// entry is one live run of the queue.
type entry struct {
	seq  string
	info entryInfo
	held bool
}

func (e entry) pinned() bool { return e.info.Pinned > 0 }

func validProject(project string) bool {
	return project != "" && project == filepath.Base(project) && !strings.ContainsAny(project, `/\`) && project != "." && project != ".."
}

// lockQueue takes the short lock on the queue, waiting until ctx ends or wait
// passes.
func lockQueue(ctx context.Context, dir string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Join(dir, entriesDir), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		rel, held, err := filelock.TryLock(filepath.Join(dir, "queue"), 0)
		if err != nil {
			return nil, fmt.Errorf("slot: lock the queue: %w", err)
		}
		if held {
			return rel, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("slot: the queue stayed locked for %s", wait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// scan lists the live entries oldest first. An entry whose lock it can take
// belongs to a run that died: it removes it. own is the entry of the caller,
// whose lock the caller holds. The caller holds the queue lock.
func scan(dir, own string) ([]entry, error) {
	files, err := os.ReadDir(filepath.Join(dir, entriesDir))
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, f := range files {
		seq, ok := strings.CutSuffix(f.Name(), ".lock")
		if !ok {
			continue
		}
		path := filepath.Join(dir, entriesDir, seq)
		if seq != own {
			rel, got, err := filelock.TryLock(path, 0)
			if err != nil {
				return nil, err
			}
			if got {
				// Its run died: the operating system let the lock go.
				rel()
				removeEntry(path)
				continue
			}
		}
		raw, err := os.ReadFile(path + ".info")
		if err != nil {
			continue // registering or leaving: not yet, or no longer, a run
		}
		e := entry{seq: seq}
		if json.Unmarshal(raw, &e.info) != nil {
			continue
		}
		_, herr := os.Stat(path + ".held")
		e.held = herr == nil
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out, nil
}

func removeEntry(path string) {
	_ = os.Remove(path + ".held")
	_ = os.Remove(path + ".info")
	_ = os.Remove(path + ".lock")
}

// register gives the request the next arrival number and an entry locked for
// the run's life. The caller holds the queue lock.
func register(dir string, req Request) (seq string, release func(), err error) {
	n := 0
	if raw, rerr := os.ReadFile(filepath.Join(dir, "next")); rerr == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	if err := os.WriteFile(filepath.Join(dir, "next"), []byte(strconv.Itoa(n+1)), 0o644); err != nil {
		return "", nil, err
	}
	seq = fmt.Sprintf("%020d", n)
	path := filepath.Join(dir, entriesDir, seq)
	rel, held, err := filelock.TryLock(path, 0)
	if err != nil {
		return "", nil, err
	}
	if !held {
		return "", nil, fmt.Errorf("slot: arrival number %s is already taken", seq)
	}
	raw, _ := json.Marshal(entryInfo{Project: req.Project, Who: req.Who, Pinned: req.Pinned})
	if err := os.WriteFile(path+".info", raw, 0o644); err != nil {
		rel()
		removeEntry(path)
		return "", nil, err
	}
	return seq, rel, nil
}

// admitted says whether the entry own is to be admitted now, given the live
// entries oldest first.
func admitted(entries []entry, own string) bool {
	var me *entry
	for i := range entries {
		if entries[i].seq == own {
			me = &entries[i]
		}
	}
	if me == nil {
		return false
	}
	if me.pinned() {
		held := 0
		for _, e := range entries {
			if e.info.Project != me.info.Project {
				continue
			}
			if e.held {
				held++
			} else if e.seq < own {
				return false // an older run of the project's own waits first
			}
		}
		return held < me.info.Pinned
	}
	for _, e := range entries {
		if e.pinned() || e.seq == own {
			continue
		}
		// This run needs the whole machine, which fits only when no other
		// run on the budget holds any of it, and nothing older waits.
		if e.held || e.seq < own {
			return false
		}
	}
	return true
}

// Take asks for the request's share of the machine and waits for it in
// arrival order. onWait, when not nil, is called once, the first time the run
// is not admitted, so the caller can say it waits. The wait has no bound of its
// own: the runs ahead are suites with a time limit of their own. Cancelling
// ctx ends the wait with ctx's error. The returned release gives the share
// back; it is safe to call more than once.
func Take(ctx context.Context, root string, req Request, onWait func()) (release func(), err error) {
	if !validProject(req.Project) {
		return nil, fmt.Errorf("slot: %q is not a project name", req.Project)
	}
	if req.Pinned < 0 {
		return nil, fmt.Errorf("slot: %d slots for project %q, want at least 1 or none", req.Pinned, req.Project)
	}
	dir := home.SlotQueueDir(root)
	unlock, err := lockQueue(ctx, dir, queueLockWait)
	if err != nil {
		return nil, err
	}
	seq, rel, err := register(dir, req)
	unlock()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, entriesDir, seq)
	var once sync.Once
	leave := func() {
		once.Do(func() {
			_ = os.Remove(path + ".held")
			_ = os.Remove(path + ".info")
			rel()
			_ = os.Remove(path + ".lock")
		})
	}
	waited := false
	for {
		ok, err := false, ctx.Err()
		if err == nil {
			ok, err = tryAdmit(ctx, dir, seq)
		}
		if err != nil {
			leave()
			return nil, err
		}
		if ok {
			return leave, nil
		}
		if !waited {
			waited = true
			if onWait != nil {
				onWait()
			}
		}
		select {
		case <-ctx.Done():
			leave()
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// tryAdmit decides, under the queue lock, whether the entry seq is admitted
// now, and marks it held when it is.
func tryAdmit(ctx context.Context, dir, seq string) (bool, error) {
	unlock, err := lockQueue(ctx, dir, queueLockWait)
	if err != nil {
		return false, err
	}
	defer unlock()
	entries, err := scan(dir, seq)
	if err != nil {
		return false, err
	}
	if !admitted(entries, seq) {
		return false, nil
	}
	if err := os.WriteFile(filepath.Join(dir, entriesDir, seq)+".held", nil, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// Hold is Take for an oracle run: it records journal.EventSlotWait when the
// run has to wait and journal.EventSlotTaken when it gets its share, through
// record, which writes the line for the event it is handed, with the summary
// a wait carries (Line). A record that fails gives the share back and returns
// its error.
func Hold(ctx context.Context, root string, req Request, record func(event, summary string) error) (release func(), err error) {
	var werr error
	rel, err := Take(ctx, root, req, func() {
		summary := ""
		if st, rerr := Read(root); rerr == nil {
			if req.Pinned > 0 {
				// A pinned run waits for its own project's count.
				st = st.forProject(req.Project)
			}
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

// Status is who holds the machine's budget and who waits for it right now.
type Status struct {
	// Holders are the Who of each run holding a share ("" when its run left no
	// name); Waiters those of the live runs waiting for one.
	Holders []string
	Waiters []string
	runs    []entry
}

func (s *Status) add(e entry) {
	s.runs = append(s.runs, e)
	if e.held {
		s.Holders = append(s.Holders, e.info.Who)
	} else {
		s.Waiters = append(s.Waiters, e.info.Who)
	}
}

// forProject is the status of one project's runs.
func (s Status) forProject(project string) Status {
	var out Status
	for _, e := range s.runs {
		if e.info.Project == project {
			out.add(e)
		}
	}
	return out
}

// Read reads the machine's queue as it is, every project's runs: a run is live
// when its entry's lock is held, so a holder or waiter that died is not
// counted. The queue is shared, so a read waits for the short lock a taker
// holds while it decides.
func Read(root string) (Status, error) {
	var s Status
	dir := home.SlotQueueDir(root)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	unlock, err := lockQueue(context.Background(), dir, queueLockWait)
	if err != nil {
		return s, err
	}
	defer unlock()
	entries, err := scan(dir, "")
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		s.add(e)
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

// Waits are the runs of ticket that wait for a share of the machine now. The
// ticket's journal lines say which runs began to wait and were not given the
// share since; the queue says which of those still wait, since a run that died
// waiting leaves its slot-wait line standing, and who holds a share and how
// many wait.
func Waits(root, ticket string, lines []journal.Line) ([]Wait, error) {
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
			if st, err = Read(root); err != nil {
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
