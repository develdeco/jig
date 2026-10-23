package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// questionIDRE is the shape of a question id.
var questionIDRE = regexp.MustCompile(`^q-\d+$`)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"answer", "answer a question: the slice it parked goes back to work", []flagSpec{
			{"as", "who the answer speaks for: person or agent (default: person at a terminal, else agent)", true},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 313,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdAnswer(e, args, stdout, stdin)
		},
	})
}

// cmdAnswer implements `jig answer [<ticket>] <qid> <text...>`. The answer is
// written to the question with its provenance, the slice the question parked
// goes back to work (building when a session holds it, else queued), an
// answer line is recorded, and the change is pushed. The person who asked is
// handed the answer by `jig ask --wait`, the Stop hook or the next prompt.
func cmdAnswer(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "answer")
	as := fs.String("as", "", "who the answer speaks for: person or agent (default: person at a terminal, else agent)")
	flags := addWorkFlags(fs, false, false)
	positionals, handled, err := parseWithText(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, qid, text, err := splitAnswerArgs(positionals)
	if err != nil {
		return renderErr(stdout, err)
	}
	provenance := *as
	switch provenance {
	case "":
		provenance = defaultProvenance(e, stdin)
	case "person", "agent":
	default:
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("--as is person or agent, not %q", provenance), Code: "VALIDATION_ERROR"})
	}

	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if err := requireOpenQuestion(w, qid); err != nil {
		return renderErr(stdout, err)
	}
	slice, err := w.St.AnswerAs(w.Ticket, qid, text, provenance)
	if err != nil {
		return renderErr(stdout, err)
	}
	state, err := resumeSlice(e, w, slice, qid, provenance, text)
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("answered", [][2]string{{"ticket", w.Ticket}, {"question", qid}, {"slice", slice}, {"provenance", provenance}, {"state", state}}),
		axi.Help(fmt.Sprintf("The session that asked gets it from `jig ask --wait`, at its next prompt or Stop; `jig status %s` shows the ticket", w.Ticket)),
	)
	return 0
}

// splitAnswerArgs reads `[<ticket>] <qid> <text...>`: the ticket is left to
// the branch when the first word is already a question id.
func splitAnswerArgs(positionals []string) (ticket, qid, text string, err error) {
	rest := positionals
	if len(rest) > 0 && !questionIDRE.MatchString(rest[0]) {
		ticket, rest = rest[0], rest[1:]
	}
	if len(rest) == 0 || !questionIDRE.MatchString(rest[0]) {
		return "", "", "", &axi.Error{
			Msg:  "jig answer needs a question id like q-001 before the answer",
			Code: "VALIDATION_ERROR",
			Help: []string{"jig answer <ticket> <question> <text...>"},
		}
	}
	qid, text = rest[0], strings.TrimSpace(strings.Join(rest[1:], " "))
	if text == "" {
		return "", "", "", &axi.Error{Msg: "missing the answer text", Code: "VALIDATION_ERROR", Help: []string{"jig answer <ticket> <question> <text...>"}}
	}
	return ticket, qid, text, nil
}

// requireOpenQuestion fails, naming what there is, when qid is not a question
// of the ticket that is open.
func requireOpenQuestion(w work, qid string) error {
	q, err := w.St.ReadQuestion(w.Ticket, qid)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("ticket %s has no question %s", w.Ticket, qid),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("`jig status %s` lists its questions", w.Ticket)},
		}
	}
	if q.Status != "open" {
		return &axi.Error{
			Msg:  fmt.Sprintf("question %s is %s, not open", qid, q.Status),
			Code: "QUESTION_NOT_OPEN",
			Help: []string{fmt.Sprintf("`jig status %s` lists its questions", w.Ticket)},
		}
	}
	return nil
}

// resumeSlice moves the slice the answered question parked back to work, if
// it was parked, records the answer line and pushes. It returns the state the
// slice is in.
func resumeSlice(e env, w work, slice, qid, provenance, text string) (string, error) {
	lines, err := journal.Read(w.St, w.Ticket)
	if err != nil {
		return "", err
	}
	_, claimed := claimHolder(lines, slice)
	stillOpen, err := otherOpenQuestion(w, slice, qid)
	if err != nil {
		return "", err
	}
	ss, err := w.St.ReadSliceState(w.Ticket, slice)
	if err != nil {
		return "", err
	}
	next := answeredState(ss, claimed, stillOpen)
	if next != ss {
		if err := w.St.WriteSliceState(w.Ticket, slice, next); err != nil {
			return "", err
		}
	}
	err = journal.Append(w.St, w.Ticket, journal.Line{
		TS: stamp(e), Slice: slice, Event: journal.EventAnswer,
		Session: w.Session, Ref: qid, Provenance: provenance, Summary: flatten(text, summaryMax),
	})
	if err != nil {
		return "", err
	}
	return next.State, w.St.Push(fmt.Sprintf("%s: slice %s answered %s", w.Ticket, slice, qid))
}

// otherOpenQuestion is the id of the latest open question of slice besides
// qid, "" when there is none.
func otherOpenQuestion(w work, slice, qid string) (string, error) {
	questions, err := w.St.ReadQuestions(w.Ticket)
	if err != nil {
		return "", err
	}
	other := ""
	for _, q := range questions {
		if q.Slice == slice && q.ID != qid && q.Status == "open" {
			other = q.ID
		}
	}
	return other, nil
}
