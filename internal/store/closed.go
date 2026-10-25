package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
)

// The reasons a ticket is closed without a pull request of its own.
const (
	// CloseCovered: another change did the work. By names it (a pull request,
	// a commit).
	CloseCovered = "covered"
	// CloseSuperseded: another ticket replaces this one. By is that ticket.
	CloseSuperseded = "superseded"
	// CloseNotPlanned: the ticket will not be done. It has no By.
	CloseNotPlanned = "not-planned"
)

// CloseReasons are the reasons a ticket can be closed for, in the order the
// help names them.
var CloseReasons = []string{CloseCovered, CloseSuperseded, CloseNotPlanned}

// closeDateLayout is how a closure's date is written: the UTC day it was
// closed.
const closeDateLayout = "2006-01-02"

// Closed is a ticket's closure, the `closed:` entry of its ticket.yaml: why
// it was closed without being built, what stands for it, and when. Who decided
// is the store's git history, which the record does not repeat. A closed
// ticket cannot be built, its issue closes on the mirror, and `jig reopen`
// removes the entry.
type Closed struct {
	// Reason is CloseCovered, CloseSuperseded or CloseNotPlanned.
	Reason string
	// By is what stands for the ticket: a reference to the change that covers
	// it (a pull request such as develdeco/jig#82, a commit) or the id of the
	// ticket that supersedes it. Absent for CloseNotPlanned.
	By string
	// Note is optional free text.
	Note string
	// At is the UTC date the ticket was closed, YYYY-MM-DD.
	At string
}

// closedFile is the wire shape of the `closed:` entry.
type closedFile struct {
	Reason string `yaml:"reason"`
	By     string `yaml:"by,omitempty"`
	Note   string `yaml:"note,omitempty"`
	At     string `yaml:"at,omitempty"`
}

// NeedsBy reports whether a closure for reason names what stands for the
// ticket.
func NeedsBy(reason string) bool {
	return reason == CloseCovered || reason == CloseSuperseded
}

// Releases reports whether a ticket closed for reason lets the tickets that
// wait for it go on: its work is done (covered) or done by another ticket
// (superseded). A ticket that will not be done (not planned) holds them, since
// they need planning again.
func Releases(reason string) bool {
	return reason == CloseCovered || reason == CloseSuperseded
}

// NewClosed is the closure `jig close` writes, dated today in UTC.
func NewClosed(reason, by, note string, now time.Time) Closed {
	return Closed{Reason: reason, By: by, Note: note, At: now.UTC().Format(closeDateLayout)}
}

// Check returns what is wrong with c as a record, one sentence each, with
// nothing read from the store: a reason that is none of the three, a missing or
// misplaced By, a date that is no date. That By of a superseded closure names a
// ticket of the store is for the caller to check.
func (c Closed) Check() []string {
	var problems []string
	known := false
	for _, r := range CloseReasons {
		if c.Reason == r {
			known = true
		}
	}
	switch {
	case !known:
		problems = append(problems, fmt.Sprintf("closed: reason %q is none of %s", c.Reason, strings.Join(CloseReasons, ", ")))
	case NeedsBy(c.Reason) && strings.TrimSpace(c.By) == "":
		problems = append(problems, fmt.Sprintf("closed: a ticket closed as %s needs by: %s", c.Reason, byMeaning(c.Reason)))
	case !NeedsBy(c.Reason) && c.By != "":
		problems = append(problems, fmt.Sprintf("closed: a ticket closed as %s has no by (it is %q)", c.Reason, c.By))
	}
	if c.At != "" {
		if _, err := time.Parse(closeDateLayout, c.At); err != nil {
			problems = append(problems, fmt.Sprintf("closed: at %q is not a date (YYYY-MM-DD)", c.At))
		}
	}
	return problems
}

// byMeaning says what By is for a reason that needs one.
func byMeaning(reason string) string {
	if reason == CloseSuperseded {
		return "the ticket that replaces it"
	}
	return "what covers it, such as a pull request or a commit"
}

// Phrase says how the ticket was closed, for a sentence: "covered by
// develdeco/jig#82", "superseded by SHOP-4", "not planned". A reason it does
// not know is given as it is.
func (c Closed) Phrase() string {
	switch c.Reason {
	case CloseCovered, CloseSuperseded:
		if c.By != "" {
			return c.Reason + " by " + c.By
		}
		return c.Reason
	case CloseNotPlanned:
		return "not planned"
	}
	return c.Reason
}

// TicketRecordRelPath is ticket's ticket.yaml relative to the store root: the
// one path a closure or a reopening stages, so a claim commits that record
// alone.
func (s *Store) TicketRecordRelPath(ticket string) string {
	return filepath.Join(s.TicketRelDir(ticket), "ticket.yaml")
}

// CloseTicket writes c as ticket's closure, leaving every other field of the
// record as it is, and creates the record when the ticket has none. A ticket
// that is closed already is refused (TICKET_ALREADY_CLOSED): reopen it first.
// A caller commits the record through Store.Claim, so it lands on the store's
// origin like an id does.
func (s *Store) CloseTicket(ticket string, c Closed) error {
	return s.updateTicket(ticket, func(t *Ticket) error {
		if t.Closed != nil {
			return &axi.Error{
				Msg:  fmt.Sprintf("%s is closed already (%s)", ticket, t.Closed.Phrase()),
				Code: "TICKET_ALREADY_CLOSED",
				Help: []string{fmt.Sprintf("Reopen it first with `jig reopen %s` to close it for another reason", ticket)},
			}
		}
		t.Closed = &c
		return nil
	})
}

// ReopenTicket removes ticket's closure, leaving every other field of the
// record as it is. A ticket that is not closed is refused (TICKET_NOT_CLOSED).
func (s *Store) ReopenTicket(ticket string) error {
	return s.updateTicket(ticket, func(t *Ticket) error {
		if t.Closed == nil {
			return &axi.Error{
				Msg:  fmt.Sprintf("%s is not closed", ticket),
				Code: "TICKET_NOT_CLOSED",
				Help: []string{fmt.Sprintf("`jig close %s --reason covered|superseded|not-planned` closes a ticket without a pull request", ticket)},
			}
		}
		t.Closed = nil
		return nil
	})
}
