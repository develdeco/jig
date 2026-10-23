package main

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

const (
	// askPollEvery is how often a waiting ask reads the question from the
	// store on this machine: an answer given here shows at once.
	askPollEvery = 250 * time.Millisecond
	// askSyncEvery is how often a waiting ask syncs the store, which is how
	// an answer given on another machine arrives.
	askSyncEvery = 5 * time.Second
	// askWaitDefault is how long --wait waits unless --timeout says.
	askWaitDefault = 10 * time.Minute
	// summaryMax is the longest text a journal line carries as its summary.
	summaryMax = 600
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"ask", "ask the person a question about a slice; the slice waits on it until answered", []flagSpec{
			{"slice", sliceFlagUsage, false},
			{"wait", "wait for the answer instead of returning at once", false},
			{"timeout", "with --wait: how long to wait, 0 for no limit (default 10m)", false},
			{"session", sessionFlagUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 312,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdAsk(e, args, stdout)
		},
	})
}

// cmdAsk implements `jig ask [<ticket>] [--slice s] [--wait] [--timeout d]
// [--session id] <question...>`. The question is written to the store, the
// slice parks on it as needs-input, and the change is pushed before anything
// waits. With --wait it then syncs and reads until the question is answered,
// and prints the answer with who it speaks for. Without text, --wait waits on
// the open question the slice already has.
func cmdAsk(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "ask")
	wait := fs.Bool("wait", false, "wait for the answer instead of returning at once")
	timeout := fs.Duration("timeout", askWaitDefault, "with --wait: how long to wait, 0 for no limit (default 10m)")
	flags := addWorkFlags(fs, true, true)
	positionals, handled, err := parseWithText(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, text := splitTicket(positionals)
	if strings.TrimSpace(text) == "" && !*wait {
		return renderErr(stdout, &axi.Error{
			Msg:  "missing the question: jig ask <question...>",
			Code: "VALIDATION_ERROR",
			Help: []string{"Add --wait with no question to wait on the one this slice has open"},
		})
	}
	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	sl, err := w.slice()
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	var q store.Question
	if strings.TrimSpace(text) != "" {
		if q, err = putQuestion(e, w, sl, strings.TrimSpace(text)); err != nil {
			return renderErr(stdout, err)
		}
	} else if q, err = openQuestionOf(w, sl); err != nil {
		return renderErr(stdout, err)
	}
	if !*wait {
		ss, err := w.St.ReadSliceState(w.Ticket, sl.ID)
		if err != nil {
			return renderErr(stdout, err)
		}
		axi.Render(stdout,
			axi.KV("asked", [][2]string{{"ticket", w.Ticket}, {"slice", sl.ID}, {"question", q.ID}, {"state", ss.State}}),
			axi.Help(fmt.Sprintf("Carry on with what the question does not block; the answer comes with `jig ask --wait --slice %s`, or the person answers with `jig answer %s %s <text>`", sl.ID, w.Ticket, q.ID)),
		)
		return 0
	}
	return waitForAnswer(e, stdout, w, sl, q.ID, *timeout)
}

// putQuestion writes the question, parks the slice on it, records the
// question line and pushes, so the person can see it before anyone waits.
func putQuestion(e env, w work, sl store.Slice, text string) (store.Question, error) {
	q, err := w.St.AddQuestion(w.Ticket, sl.ID, text)
	if err != nil {
		return store.Question{}, err
	}
	ss, err := w.St.ReadSliceState(w.Ticket, sl.ID)
	if err != nil {
		return store.Question{}, err
	}
	if next := askedState(ss, q.ID); next != ss {
		if err := w.St.WriteSliceState(w.Ticket, sl.ID, next); err != nil {
			return store.Question{}, err
		}
	}
	err = journal.Append(w.St, w.Ticket, journal.Line{
		TS: stamp(e), Slice: sl.ID, Event: journal.EventQuestion,
		Session: w.Session, Ref: q.ID, Provenance: "agent", Summary: flatten(text, summaryMax),
	})
	if err != nil {
		return store.Question{}, err
	}
	return q, w.St.Push(fmt.Sprintf("%s: slice %s asks %s", w.Ticket, sl.ID, q.ID))
}

// openQuestionOf is the latest open question about the slice.
func openQuestionOf(w work, sl store.Slice) (store.Question, error) {
	questions, err := w.St.ReadQuestions(w.Ticket)
	if err != nil {
		return store.Question{}, err
	}
	for i := len(questions) - 1; i >= 0; i-- {
		if questions[i].Slice == sl.ID && questions[i].Status == "open" {
			return questions[i], nil
		}
	}
	return store.Question{}, &axi.Error{
		Msg:  fmt.Sprintf("slice %s of %s has no open question to wait on", sl.ID, w.Ticket),
		Code: "VALIDATION_ERROR",
		Help: []string{fmt.Sprintf("Ask one: `jig ask --slice %s <question...>`", sl.ID)},
	}
}

// flatten is s on one line, cut to at most max bytes at a character boundary.
func flatten(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
