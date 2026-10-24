package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"recall", "show what planning should see first: the open tickets and their edges, what comes near a query, the charts' fog, platform documents and past answers", []flagSpec{
			{"json", "print one JSON object instead of blocks", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 380,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdRecall(e, args, stdout)
		},
	})
}

// cmdRecall implements `jig recall [<query...>] [--json]`: what a planner
// should know before it writes a ticket. It lists the open tickets with their
// state and the edges between them, names the open tickets whose title or
// body comes near the query, and brings the not-yet-specified part of each
// chart, the platform documents (the lines that mention the query among
// them) and the answers the ledger holds. It reads the store as it is on this
// machine and writes nothing.
func cmdRecall(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "recall")
	jsonOut := fs.Bool("json", false, "print one JSON object instead of blocks")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	positionals, handled, err := parseWithText(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	query := strings.Join(positionals, " ")

	st, cfg, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	tickets, err := recallTickets(st)
	if err != nil {
		return renderErr(stdout, err)
	}
	material, err := recall.Read(st, cfg.Platform)
	if err != nil {
		return renderErr(stdout, err)
	}
	report := recall.Build(tickets, material, query)

	if *jsonOut {
		data, err := json.Marshal(report)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	blocks := report.Blocks()
	blocks = append(blocks, axi.Help(recallHelp(report)...))
	axi.Render(stdout, blocks...)
	return 0
}

// recallTickets reads every ticket of the store as a planner sees it: its
// record, its blockers and the state the readiness rules give it.
func recallTickets(st *store.Store) ([]recall.Ticket, error) {
	ids, err := st.TicketIDs()
	if err != nil {
		return nil, err
	}
	datas := make([]ticketData, len(ids))
	inputs := make([]ready.Ticket, len(ids))
	for i, id := range ids {
		datas[i] = readTicketDataOrProblem(st, id)
		inputs[i] = datas[i].in
	}
	out := make([]recall.Ticket, len(ids))
	for i, s := range ready.Tickets(inputs) {
		t := recall.Ticket{
			ID: s.ID, Title: datas[i].title, Body: datas[i].rec.Body,
			State: s.State, Shipped: s.State == ready.StateDone,
		}
		if datas[i].problem != "" {
			t.State = stateInvalid
		}
		for _, b := range datas[i].in.Blockers {
			t.BlockedBy = append(t.BlockedBy, recall.Blocker{Ticket: b.Ticket, Kind: b.Kind})
		}
		out[i] = t
	}
	return out, nil
}

// recallHelp is the next step after a report: what to do with a duplicate, or
// how to ask about one, and where the brief goes from here.
func recallHelp(r recall.Report) []string {
	var help []string
	switch {
	case len(r.Duplicates) > 0:
		d := r.Duplicates[0]
		help = append(help, fmt.Sprintf("%s comes near the query (%.2f, in its %s): read it before minting another ticket, and extend it or block on it if it is the same work", d.Ticket, d.Score, d.In))
	case r.Query == "":
		help = append(help, "Name the work in words, `jig recall <words...>`, to find the open tickets that come near it")
	}
	return append(help, "Draft the brief and slices with the intake skill, then `jig validate <ticket>` checks them and records the approval")
}
