package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// waitForAnswer waits for question qid to be answered and prints the answer.
// It reads the question from the store on this machine every askPollEvery,
// so an answer given here shows at once, and syncs the store every
// askSyncEvery, which is how one given elsewhere arrives. A sync that fails
// does not end the wait (the answer may still come, from here); the last
// failure is named if the wait ends without one. timeout 0 waits for ever; the
// first interrupt ends the wait too.
func waitForAnswer(e env, stdout io.Writer, w work, sl store.Slice, qid string, timeout time.Duration) int {
	start := e.now()
	nextSync := start.Add(askSyncEvery)
	var syncErr error
	for {
		q, err := w.St.ReadQuestion(w.Ticket, qid)
		if err != nil {
			return renderErr(stdout, err)
		}
		switch q.Status {
		case "answered":
			return printAnswer(e, stdout, w, sl, q)
		case "superseded":
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("question %s no longer counts: the slice was requeued after a brief amendment", qid),
				Code: "QUESTION_SUPERSEDED",
				Help: []string{fmt.Sprintf("`jig context %s --slice %s` shows the slice as it stands", w.Ticket, sl.ID)},
			})
		}

		now := e.now()
		if timeout > 0 && now.Sub(start) >= timeout {
			return renderErr(stdout, noAnswerYet(w, sl, qid, timeout, syncErr))
		}
		select {
		case <-e.ctx.Done():
			return renderInterrupted(stdout, qid)
		case <-time.After(askPollEvery):
		}
		if now = e.now(); !now.Before(nextSync) {
			syncErr = w.St.Sync()
			nextSync = now.Add(askSyncEvery)
		}
	}
}

// noAnswerYet is the NEEDS_INPUT error of a wait that timed out: the question
// stays open, and the help says how to keep waiting.
func noAnswerYet(w work, sl store.Slice, qid string, timeout time.Duration, syncErr error) *axi.Error {
	help := []string{
		fmt.Sprintf("Wait again: `jig ask --wait --slice %s`", sl.ID),
		fmt.Sprintf("The person answers with `jig answer %s %s <text>`", w.Ticket, qid),
	}
	if syncErr != nil {
		help = append(help, "The last sync of the store failed, so an answer given elsewhere may not have arrived: "+flatten(syncErr.Error(), 200))
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("no answer to %s after %s; it stays open", qid, timeout),
		Code: axi.NeedsInput,
		Help: help,
	}
}

// renderInterrupted reports a wait the first interrupt ended and returns the
// status of an interrupt.
func renderInterrupted(stdout io.Writer, qid string) int {
	renderErr(stdout, &axi.Error{
		Msg:  fmt.Sprintf("interrupted while waiting for the answer to %s; it stays open", qid),
		Code: "INTERRUPTED",
	})
	return exitInterrupted
}

// printAnswer prints the answer a wait ended on, with who it speaks for, and
// notes in the machine ledger that this session has been given it, so a hook
// that delivers answers does not give it again.
func printAnswer(e env, stdout io.Writer, w work, sl store.Slice, q store.Question) int {
	if w.Session != "" {
		_ = ledger.Append(w.JigHome, journal.Line{
			TS: stamp(e), Ticket: w.Ticket, Slice: sl.ID, Event: journal.EventNoteDelivered,
			Session: w.Session, Ref: q.ID, Provenance: q.Provenance,
		})
	}
	provenance := q.Provenance
	if provenance == "" {
		provenance = "unknown"
	}
	head := axi.KV("answer", [][2]string{
		{"ticket", w.Ticket}, {"slice", sl.ID}, {"question", q.ID}, {"provenance", provenance},
	})
	text := axi.Block{Header: "text:"}
	for _, line := range splitLines(q.Answer) {
		text.Lines = append(text.Lines, indentLine(line))
	}
	axi.Render(stdout, head, text.String(), axi.Help(fmt.Sprintf(
		"Carry on with slice %s using this answer; `jig context %s --slice %s` has it with the rest", sl.ID, w.Ticket, sl.ID)))
	return 0
}

// splitLines is s as lines, without a trailing empty one.
func splitLines(s string) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// indentLine is line indented two spaces, an empty one left empty.
func indentLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return ""
	}
	return "  " + line
}
