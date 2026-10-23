package observe

import (
	"fmt"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verify"
)

// The sources every jig has: what became of the commits the session made, and
// the answers to the questions it asked.
func init() {
	RegisterNoteSource("verification", verificationNotes)
	RegisterNoteSource("answer", answerNotes)
}

// deliverable reports whether the built-in sources speak at a kind of event.
func deliverable(kind string) bool {
	return kind == KindTool || kind == KindPrompt || kind == KindStop
}

// verificationNotes tells the session how its commits fared: a verification
// that passed (advisory), and one that failed or could not run (enforced),
// for as long as no later verification of the slice has passed and no newer
// commit of the slice has an outcome (workers finish in any order, so the
// journal's order is not the commits'). Only outcomes recorded since the
// session began count.
func verificationNotes(c NoteCtx) ([]Note, error) {
	if !deliverable(c.Event.Kind) || c.Store == nil || c.Where.Ticket == "" {
		return nil, nil
	}
	lines, err := c.Journal()
	if err != nil {
		return nil, err
	}
	lastPass := map[string]int{} // slice -> index of its latest verified line
	for i, l := range lines {
		if l.Event == journal.EventVerified {
			lastPass[l.Slice] = i
		}
	}
	ancestry := verify.GitAncestry(c.Where.RepoDir)
	var notes []Note
	for i, l := range lines {
		if l.Commit == "" || !since(l.TS, c.Since) {
			continue
		}
		sha := short12(l.Commit)
		failed := l.Event == journal.EventVerifyError || (l.Event == journal.EventOracle && l.Outcome == "fail")
		switch {
		case l.Event == journal.EventVerified:
			v := journal.VerificationState(lines[:i+1], l.Commit, l.Slice)
			text := fmt.Sprintf("verified %s for slice %s (attempt %d", sha, l.Slice, l.Attempt)
			if v.Seconds > 0 {
				text += fmt.Sprintf(", %ds", v.Seconds)
			}
			notes = append(notes, Note{Ref: fmt.Sprintf("verified:%s:%s:%d", sha, l.Slice, l.Attempt), Text: text + ")"})
		case failed:
			if at, ok := lastPass[l.Slice]; ok && at > i {
				continue // a later verification passed: the failure is history
			}
			if verify.Superseded(lines, l.Slice, l.Commit, ancestry) {
				continue // a newer commit of the slice has its own outcome
			}
			what := "FAILED"
			if l.Event == journal.EventVerifyError {
				what = "could not run"
			}
			text := fmt.Sprintf("verification %s for slice %s at %s (attempt %d)", what, l.Slice, sha, l.Attempt)
			if l.Summary != "" {
				text += ": " + l.Summary
			}
			notes = append(notes, Note{Ref: fmt.Sprintf("%s:%s:%s:%d", l.Event, sha, l.Slice, l.Attempt), Text: text, Enforce: true})
		}
	}
	return notes, nil
}

// answerNotes gives a session the answer to a question it asked: a question
// line of the journal that names the session and a question, and an answer
// line for that question made since the session began.
func answerNotes(c NoteCtx) ([]Note, error) {
	if !deliverable(c.Event.Kind) || c.Store == nil || c.Where.Ticket == "" || c.Event.Session == "" {
		return nil, nil
	}
	lines, err := c.Journal()
	if err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, l := range lines {
		if l.Event == journal.EventQuestion && l.Ref != "" && l.Session == c.Event.Session {
			asked[l.Ref] = true
		}
	}
	if len(asked) == 0 {
		return nil, nil
	}
	questions, err := c.Store.ReadQuestions(c.Where.Ticket)
	if err != nil {
		return nil, err
	}
	var notes []Note
	for _, l := range lines {
		if l.Event != journal.EventAnswer || !asked[l.Ref] || !since(l.TS, c.Since) {
			continue
		}
		body := l.Summary
		for _, q := range questions {
			if q.ID == l.Ref && q.Answer != "" {
				body = q.Answer
			}
		}
		by := ""
		if l.Provenance != "" {
			by = " by the " + l.Provenance
		}
		notes = append(notes, Note{Ref: "answer:" + l.Ref, Text: fmt.Sprintf("%s was answered%s: %s", l.Ref, by, oneLine(body, 600))})
	}
	return notes, nil
}

// since reports whether the stamp ts is at or after t. A zero t admits
// nothing (the session start is unknown, so nothing is known to be its own),
// and a stamp that does not parse is not admitted either.
func since(ts string, t time.Time) bool {
	if t.IsZero() {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	return err == nil && !at.Before(t)
}

// short12 is the first twelve characters of a commit id.
func short12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// oneLine flattens s to one line of at most max bytes, cut at a rune
// boundary.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
