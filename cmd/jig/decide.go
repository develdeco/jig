package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// decideUsage is the shape of the verb, named by each refusal.
const decideUsage = "jig decide [<ticket>] [--slice <slice>] <question...> --chose <answer>"

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"decide", "record a judgment call you made yourself: what was ambiguous and what you chose", []flagSpec{
			{"chose", "what was chosen, and why (required)", false},
			{"slice", sliceFlagUsage, false},
			{"session", sessionFlagUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 315,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdDecide(e, args, stdout)
		},
	})
}

// cmdDecide implements `jig decide [<ticket>] [--slice s] [--session id]
// <question...> --chose <answer>`. A call is the opposite of an ask: the
// builder answers it itself, so it is written already answered, parks and
// claims nothing, changes no slice state and waits on no one. The slice is
// --slice, else the one the session holds, else none (the whole ticket), and
// the ticket may be in any phase.
func cmdDecide(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "decide")
	chose := fs.String("chose", "", "what was chosen, and why (required)")
	flags := addWorkFlags(fs, true, true)
	positionals, handled, err := parseWithText(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, text := splitTicket(positionals)
	text, answer := strings.TrimSpace(text), strings.TrimSpace(*chose)
	if text == "" {
		return renderErr(stdout, &axi.Error{
			Msg: "missing the question: " + decideUsage, Code: "VALIDATION_ERROR",
			Help: []string{"The question is what was ambiguous; --chose is what you picked and why"},
		})
	}
	if answer == "" {
		return renderErr(stdout, &axi.Error{
			Msg: "missing --chose: " + decideUsage, Code: "VALIDATION_ERROR",
			Help: []string{"Say what you chose, and why, after --chose"},
		})
	}
	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	slice, err := w.callSlice()
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	q, err := w.St.AddCall(w.Ticket, slice, text, answer)
	if err != nil {
		return renderErr(stdout, err)
	}
	err = journal.Append(w.St, w.Ticket, journal.Line{
		TS: stamp(e), Slice: slice, Event: journal.EventCall,
		Session: w.Session, Ref: q.ID, Provenance: "agent", Summary: flatten(text+" - chose: "+answer, summaryMax),
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Push(fmt.Sprintf("%s: call %s", w.Ticket, q.ID)); err != nil {
		return renderErr(stdout, err)
	}
	shown := slice
	if shown == "" {
		shown = "-"
	}
	axi.Render(stdout,
		axi.KV("decided", [][2]string{{"ticket", w.Ticket}, {"question", q.ID}, {"slice", shown}, {"kind", store.KindCall}}),
		axi.Help(fmt.Sprintf("The call is on record for the reviewer and the ledger; `jig context %s` lists it with the questions", w.Ticket)),
	)
	return 0
}

// callSlice is the slice a call is about: the one named or held, which must be
// a slice of the ticket, or "" for the whole ticket when there is none.
func (w work) callSlice() (string, error) {
	if w.Where.Slice == "" {
		return "", nil
	}
	sl, err := w.slice()
	if err != nil {
		return "", err
	}
	return sl.ID, nil
}
