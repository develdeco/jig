package events

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// Journal names one ticket journal a Scope reads.
type Journal struct {
	// Store is the project name put on the journal's envelopes.
	Store string
	// Ticket is the ticket the journal is of.
	Ticket string
	// Path is the journal.ndjson file (journal.Path). It need not exist yet.
	Path string
}

// Scope says what a stream covers.
type Scope struct {
	// Journals lists the journals in scope, in the order that breaks a tie
	// between two lines of one instant. It is called at every poll, so a
	// ticket made while a follower runs is read from then on. A nil Journals
	// reads none.
	Journals func() ([]Journal, error)
	// Ledger is the jig home root whose machine ledger is read ("" for no
	// ledger).
	Ledger string
	// AllLedger puts every ledger line in scope. Otherwise a ledger line is
	// in scope when it is about a ticket of the journals: it names that
	// ticket, or it names none and its session was last bound to that ticket
	// by an earlier line (a tool line carries a session and no ticket).
	AllLedger bool
}

// Read returns the lines in scope as they are now, oldest first, leaving out
// those stamped before since (the zero time leaves out none). A line that is
// not JSON is skipped, as is a blank one: one bad line never hides the rest.
// Ties go to journal lines before ledger lines, then to file order. A file
// that cannot be read fails the read, so a short stream is never passed off
// as a whole one.
func Read(scope Scope, since time.Time) ([]Envelope, error) {
	envs, err := NewReader(scope, since).Poll()
	if err != nil {
		return nil, err
	}
	return envs, nil
}

// Reader reads a Scope again and again, handing back only what it has not
// handed back before. It keeps no file open between polls. It is for one
// goroutine.
type Reader struct {
	scope Scope
	since time.Time

	files     map[string]*fileState
	ledgerOff int64
	// bound is the ticket each session was last seen on.
	bound map[string]string
}

// fileState is what a Reader remembers of one journal file.
type fileState struct {
	// size and mod are the file as the last read took it; a poll that finds
	// them unchanged does not read the file again.
	size int64
	mod  time.Time
	// seen counts, per line (by hash), the copies already handed back, so
	// a file rewritten whole - by Append, or by a merge that reorders
	// lines - gives back only the lines that are new.
	seen map[[16]byte]int
}

// NewReader returns a Reader of scope that leaves out lines stamped before
// since (zero: none).
func NewReader(scope Scope, since time.Time) *Reader {
	return &Reader{
		scope: scope,
		since: since,
		files: map[string]*fileState{},
		bound: map[string]string{},
	}
}

// item is an envelope with the instant it sorts by.
type item struct {
	env Envelope
	at  time.Time
}

// Poll reads what the scope holds now and returns the lines not returned by
// an earlier Poll, oldest first. The first Poll is the whole stream. When a
// file cannot be read it returns the lines of the others with the first
// error; the file is tried again on the next Poll. A Poll that cannot list
// the journals reads no ledger either, since which ledger lines are in scope
// depends on them, and leaves the ledger offset where it was.
func (r *Reader) Poll() ([]Envelope, error) {
	var (
		items    []item
		firstErr error
	)
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	var journals []Journal
	if r.scope.Journals != nil {
		var err error
		if journals, err = r.scope.Journals(); err != nil {
			return nil, fmt.Errorf("events: list the journals: %w", err)
		}
	}
	tickets := make(map[string]bool, len(journals))
	for _, j := range journals {
		tickets[j.Ticket] = true
		got, err := r.readJournal(j)
		if err != nil {
			note(err)
			continue
		}
		items = append(items, got...)
	}
	if r.scope.Ledger != "" {
		got, err := r.readLedger(tickets)
		if err != nil {
			note(err)
		}
		items = append(items, got...)
	}

	// Stable: a tie keeps the order the items were gathered in, which is
	// journals (in the order listed, each in file order) and then the ledger.
	sort.SliceStable(items, func(a, b int) bool { return items[a].at.Before(items[b].at) })
	envs := make([]Envelope, len(items))
	for i, it := range items {
		envs[i] = it.env
	}
	return envs, firstErr
}

// keep reports whether a line stamped at is on the right side of since.
func (r *Reader) keep(at time.Time) bool {
	return r.since.IsZero() || !at.Before(r.since)
}

// readJournal returns the lines of j not returned before. It reads the whole
// file, closes it, and tells the new lines from the old by their hash: the
// file is replaced on each append and may be rewritten by a merge, so an
// offset means nothing.
func (r *Reader) readJournal(j Journal) ([]item, error) {
	info, err := os.Stat(j.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: stat %s: %w", j.Path, err)
	}
	fs := r.files[j.Path]
	if fs == nil {
		fs = &fileState{seen: map[[16]byte]int{}}
		r.files[j.Path] = fs
	} else if info.Size() == fs.size && info.ModTime().Equal(fs.mod) {
		return nil, nil
	}
	data, err := os.ReadFile(j.Path)
	if err != nil {
		return nil, fmt.Errorf("events: read %s: %w", j.Path, err)
	}
	// Taken before the read: a change after it shows up as a difference.
	fs.size, fs.mod = info.Size(), info.ModTime()

	var (
		out    []item
		counts = map[[16]byte]int{}
	)
	for len(data) > 0 {
		var raw []byte
		raw, data, _ = bytes.Cut(data, []byte("\n"))
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		key := [16]byte(sum[:16])
		counts[key]++
		if counts[key] <= fs.seen[key] {
			continue
		}
		var l journal.Line
		if !bytes.HasPrefix(raw, []byte("{")) || json.Unmarshal(raw, &l) != nil {
			// Not a line (yet): a half-written one will be, next poll.
			counts[key]--
			continue
		}
		fs.seen[key] = counts[key]
		env := Envelope{Src: SrcJournal, Store: j.Store, Line: l}
		at, _ := env.At()
		if r.keep(at) {
			out = append(out, item{env: env, at: at})
		}
	}
	return out, nil
}

// readLedger returns the ledger lines after the last offset that are in
// scope. Every line moves the session bindings, in file order, whether or
// not it is returned.
func (r *Reader) readLedger(tickets map[string]bool) ([]item, error) {
	lines, off, err := ledger.ReadFrom(r.scope.Ledger, r.ledgerOff)
	if err != nil {
		return nil, err
	}
	r.ledgerOff = off
	var out []item
	for _, l := range lines {
		if l.Session != "" && l.Ticket != "" {
			r.bound[l.Session] = l.Ticket
		}
		if !r.scope.AllLedger {
			about := l.Ticket
			if about == "" && l.Session != "" {
				about = r.bound[l.Session]
			}
			if about == "" || !tickets[about] {
				continue
			}
		}
		env := Envelope{Src: SrcLedger, Line: l}
		at, _ := env.At()
		if r.keep(at) {
			out = append(out, item{env: env, at: at})
		}
	}
	return out, nil
}
