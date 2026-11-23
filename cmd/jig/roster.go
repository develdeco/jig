package main

import (
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// nextRoster is what `jig next --json --roster` prints: every ticket's row as
// `jig next --json` prints it, and the charts of the store, and nothing a
// screen does not read. The rows are made by readStoreRows, the one function
// both verbs use, so they cannot differ.
type nextRoster struct {
	Tickets []nextRow      `json:"tickets"`
	Charts  []nextChartRow `json:"charts,omitempty"`
}

// storeRows are the rows of a listing of the whole store and what the full
// listing orders its ready tickets by.
type storeRows struct {
	rows     []nextRow
	ranked   []ready.Ranked
	slicesOf map[string][]store.Slice
}

// readStoreRows reads every ticket of the store once and makes its row: its
// state and what blocks it, and its digest, the one reading the screens take
// their words from. What every ticket shares (the chart index, the merge
// reader, the repos) is read once and looked up; the tickets' own files are
// read concurrently, at most one worker for each processor, with the rows kept
// in id order.
func readStoreRows(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) (storeRows, error) {
	ids, err := st.Tickets()
	if err != nil {
		return storeRows{}, err
	}
	datas := make([]ticketData, len(ids))
	inputs := make([]ready.Ticket, len(ids))
	forEachBounded(len(ids), func(i int) {
		datas[i] = readTicketDataOrProblem(st, ids[i])
		settleRebuilding(st, merges, &datas[i].in)
		inputs[i] = datas[i].in
	})
	readMerges(st, merges, inputs, false)
	digestFrom := digestMerges(merges)
	states := ready.Tickets(inputs)
	rows := make([]nextRow, len(states))
	forEachBounded(len(states), func(i int) {
		s := states[i]
		row := nextRow{ID: s.ID, Title: datas[i].title, State: s.State, Rank: datas[i].rec.Rank, Ready: s.Ready}
		if c := datas[i].rec.Closed; c != nil {
			row.Closed = c.Phrase()
		}
		if row.Ready == nil {
			row.Ready = []string{}
		}
		for _, b := range s.Blocked {
			row.BlockedBy = append(row.BlockedBy, blockerFact(b, s.Why[b.Ticket], inputs))
		}
		d, updated := rosterDigest(st, cfg, mp, digestFrom, datas[i], s.State, row.BlockedBy)
		row.Digest = &d
		row.Updated = updated
		switch {
		case datas[i].problem != "":
			row.State, row.Problem, row.Ready = stateInvalid, datas[i].problem, []string{}
		case datas[i].rec.KindOrDefault() != store.KindTask:
			k := datas[i].rec.KindOrDefault()
			row.State, row.Kind, row.Ready = k, k, []string{}
		}
		rows[i] = row
	})
	out := storeRows{rows: rows, slicesOf: map[string][]store.Slice{}}
	for i, s := range states {
		if datas[i].problem != "" || datas[i].rec.KindOrDefault() != store.KindTask {
			continue
		}
		if s.State == ready.StateReady {
			out.ranked = append(out.ranked, ready.Ranked{ID: s.ID, Rank: datas[i].rec.Rank})
			out.slicesOf[s.ID] = datas[i].in.Slices
		}
	}
	return out, nil
}

// nextForRoster is the roster: the rows of the store and its charts.
func nextForRoster(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) (nextRoster, error) {
	rows, err := readStoreRows(st, cfg, mp, merges)
	if err != nil {
		return nextRoster{}, err
	}
	return nextRoster{Tickets: rows.rows, Charts: chartRows(st)}, nil
}

// forEachBounded calls fn for each index below n, concurrently, with at most as
// many running as there are processors. fn writes only to its own index of the
// slices it fills; what it reads is shared read-only or guarded.
func forEachBounded(n int, fn func(i int)) {
	workers := min(n, max(1, runtime.GOMAXPROCS(0)))
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// newestLine is the instant of the newest line of a journal in RFC 3339,
// "" when it has none.
func newestLine(lines []journal.Line) string {
	var newest time.Time
	for _, l := range lines {
		if t, err := time.Parse(time.RFC3339Nano, l.TS); err == nil && t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return ""
	}
	return newest.UTC().Format(time.RFC3339)
}

// readScope opens the reads a listing of the whole store makes: the chart index
// stands for the run, each repo's branches are listed once and answers about
// ancestry are kept on this machine under the jig home. The returned function
// ends it.
func readScope(st *store.Store, mp project.MachineProject, jigHome string) (end func()) {
	unpin := st.PinCharts()
	var dirs []string
	seen := map[string]bool{}
	names := make([]string, 0, len(mp.Clones))
	for name := range mp.Clones {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if dir := mp.Clones[name]; dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	ancestry := ""
	if jigHome != "" {
		ancestry = home.AncestryPath(jigHome)
	}
	endGit := gitx.BeginScope(ancestry, dirs...)
	return func() {
		endGit()
		unpin()
	}
}

// rosterRefusal is the refusal for what `jig next --roster` does not
// read: it is the rows of the whole store, and a screen reads nothing else.
func rosterRefusal(ticket string, forSet, topology, ticketsSet, jsonOut bool) error {
	switch {
	case ticket != "":
		return &axi.Error{Msg: "--roster lists the whole store: it takes no ticket or chart (`jig next <ticket>` reads one)", Code: "VALIDATION_ERROR"}
	case forSet:
		return &axi.Error{Msg: "--roster takes no --for: it lists every ticket's row, not one phase", Code: "VALIDATION_ERROR"}
	case topology:
		return &axi.Error{Msg: "--roster takes no --topology: it lists every ticket's row, not one ticket's slices", Code: "VALIDATION_ERROR"}
	case ticketsSet:
		return &axi.Error{Msg: "--roster takes no --tickets: it lists the whole store, not a chart's run", Code: "VALIDATION_ERROR"}
	case !jsonOut:
		return &axi.Error{Msg: "--roster prints JSON: use it with --json", Code: "VALIDATION_ERROR"}
	}
	return nil
}
