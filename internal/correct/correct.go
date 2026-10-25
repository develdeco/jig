package correct

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// The kinds of correction, as a correction line names them in its outcome.
const (
	KindStall = "stall"
	KindBrief = "brief-drift"
	KindScope = "scope-drift"
	KindSlice = "slice-missing"
)

// Input is what Check is given: the session and where it works, the ticket
// store, and what the event that called it just saw.
type Input struct {
	Store  *store.Store
	Ticket string

	// Session, Agent and Harness say whose correction lines these are.
	Session, Agent, Harness string
	// Slice is the slice the session works on, "" for none.
	Slice string
	// Cwd is the directory of the event, in the repository the session
	// commits to, and Repo that repository's name in the project ("" when it
	// is none of its repos).
	Cwd  string
	Repo string

	// Now is the clock; Since is when the session began (zero when unknown:
	// nothing is then known to be the session own).
	Now, Since time.Time
	// Commit is the commit HEAD moved to during this event, "" for none.
	Commit string
	// Unattended is true when no one is watching the session: a stall also
	// marks the slice stalled.
	Unattended bool
	// Journal reads the ticket journal.
	Journal func() ([]journal.Line, error)
}

// A Note is one thing the session has to hear. Each is the kind of note an
// unattended session is held back at Stop for.
type Note struct {
	// Ref is the fingerprint of the fact: the same every time it is found.
	Ref string
	// Text is what the session reads, one line.
	Text string
	// Kind is one of the Kind constants.
	Kind string
}

// fact is a condition found true, with what its correction line records.
type fact struct {
	note Note
	// slice, commit, files and signature fill the correction line.
	slice, commit, signature string
	files                    []string
	// stall is the stall a state change is for, nil for the other kinds.
	stall *Stall
}

// maxLineFiles caps the files a correction line lists: the journal is read
// whole by every command.
const maxLineFiles = 20

// errNone is what a detector returns when it found nothing; add drops it.
var errNone = errors.New("nothing found")

// Check finds the corrections the ticket needs and returns the notes to give
// the session. It is called at every tool call, prompt and stop of a session,
// so it holds nothing: what it knows is the journal, the slices, the brief
// and the commit it is given, and a correction line is written the first time
// a fact (a Ref) is found, never again.
//
//   - A stall (stall.go) is found in the journal each time, while its latest
//     failure was made in this session. Unattended, its slice is also marked
//     stalled, once, when the fact is first found.
//   - A brief drift (brief.go) is found from the brief and the slices each
//     time.
//   - A scope drift (scope.go) is found for the commit the event made, and
//     the correction line it leaves is what offers it again at the events that
//     follow, so a Stop can still hold the session back for it.
//   - A missing slice (slice.go) is found for the same commit when it names no
//     slice, and the slices its files point to are offered as the likely one.
//     It is offered again like a scope drift, until the commit is amended away
//     or verified for a slice.
//
// A detector that fails costs only its own note; the notes of the others are
// returned with the error that joins what failed. When writing the record of
// a fact fails, the fact is not returned: the next call finds it again.
func Check(in Input) ([]Note, error) {
	lines, err := in.Journal()
	if err != nil {
		return nil, err
	}
	fired := map[string]bool{}
	for _, l := range lines {
		if l.Event == journal.EventCorrection && l.Ref != "" {
			fired[l.Ref] = true
		}
	}

	var errs []error
	var facts []fact
	add := func(f fact, err error) {
		if errors.Is(err, errNone) {
			return
		}
		if err != nil {
			errs = append(errs, err)
			return
		}
		for _, g := range facts {
			if g.note.Ref == f.note.Ref {
				return
			}
		}
		facts = append(facts, f)
	}

	for _, s := range Stalls(lines) {
		if after(s.TS, in.Since) {
			add(fact{
				note:  Note{Ref: s.Ref(), Kind: KindStall, Text: stallText(in.Ticket, s, in.Unattended)},
				slice: s.Slice, commit: s.Commit, signature: s.Signature, stall: &s,
			}, nil)
		}
	}

	all, err := in.Store.ReadSlices(in.Ticket)
	if err != nil {
		errs = append(errs, err)
	} else {
		add(briefFact(in, all, lines))
		if in.Commit != "" {
			add(scopeFact(in, all))
			add(sliceFact(in, all))
		}
	}
	// What this session was told of earlier commits is in the journal.
	for _, l := range lines {
		if l.Event != journal.EventCorrection || in.Session == "" || l.Session != in.Session || !after(l.TS, in.Since) {
			continue
		}
		switch l.Outcome {
		case KindScope:
			add(fact{note: Note{Ref: l.Ref, Kind: KindScope, Text: l.Summary}}, nil)
		case KindSlice:
			if stillUnsliced(in, l.Commit, lines) {
				add(fact{note: Note{Ref: l.Ref, Kind: KindSlice, Text: l.Summary}}, nil)
			}
		}
	}

	var notes []Note
	for _, f := range facts {
		if !fired[f.note.Ref] {
			if err := fire(in, f); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		notes = append(notes, f.note)
	}
	return notes, errors.Join(errs...)
}

// briefFact finds the brief drift of the ticket, if any. A ticket with no
// brief has none.
func briefFact(in Input, all []store.Slice, lines []journal.Line) (fact, error) {
	verified := Verified(lines)
	if len(verified) == 0 {
		return fact{}, errNone
	}
	data, err := os.ReadFile(filepath.Join(in.Store.TicketDir(in.Ticket), "brief.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return fact{}, errNone
		}
		return fact{}, err
	}
	drifts := BriefDrift(data, all, verified)
	if len(drifts) == 0 {
		return fact{}, errNone
	}
	return fact{note: Note{Ref: driftRef(drifts), Kind: KindBrief, Text: driftText(in.Ticket, drifts)}}, nil
}

// scopeFact finds the scope drift of the commit the event made, if any.
func scopeFact(in Input, all []store.Slice) (fact, error) {
	if in.Cwd == "" {
		return fact{}, errNone
	}
	d, drifted, err := Scope(in.Cwd, in.Repo, in.Commit, all)
	if err != nil {
		return fact{}, err
	}
	if !drifted {
		return fact{}, errNone
	}
	return fact{
		note:  Note{Ref: d.Ref(), Kind: KindScope, Text: scopeText(in.Ticket, d)},
		slice: in.Slice, commit: d.Commit, files: d.Files,
	}, nil
}

// fire records a fact the first time it is found: the slice marked stalled
// when it is a stall and no one is watching, then the correction line. The
// state goes first, so that a failure between the two is found again and the
// state move (which does nothing the second time) is not lost.
func fire(in Input, f fact) error {
	if f.stall != nil && in.Unattended {
		if err := markStalled(in, *f.stall); err != nil {
			return err
		}
	}
	line := journal.Line{
		TS:         in.Now.UTC().Format(time.RFC3339Nano),
		Slice:      f.slice,
		Event:      journal.EventCorrection,
		Outcome:    f.note.Kind,
		Session:    in.Session,
		Agent:      in.Agent,
		Harness:    in.Harness,
		Ref:        f.note.Ref,
		Provenance: "jig",
		Summary:    oneLine(f.note.Text, 600),
		Commit:     f.commit,
		Signature:  f.signature,
		Files:      f.files,
	}
	if len(line.Files) > maxLineFiles {
		line.Files = line.Files[:maxLineFiles]
	}
	return journal.Append(in.Store, in.Ticket, line)
}

// markStalled moves the slice of a stall to stalled, under the checkpoint
// lock of the store so that a claim or a verification made meanwhile by
// another process is not overwritten with a stale copy. The file stays in the
// store clone for the next checkpoint to push: a hook does not wait on the
// network.
func markStalled(in Input, s Stall) error {
	release, err := in.Store.LockCheckpoint()
	if err != nil {
		return err
	}
	defer release()
	ss, err := in.Store.ReadSliceState(in.Ticket, s.Slice)
	if err != nil {
		return err
	}
	next, changed := Stalled(ss, s)
	if !changed {
		return nil
	}
	if err := in.Store.WriteSliceState(in.Ticket, s.Slice, next); err != nil {
		return fmt.Errorf("correct: mark slice %s stalled: %w", s.Slice, err)
	}
	return nil
}

// after reports whether the stamp ts is at or after t. A zero t admits
// nothing (the session start is unknown, so nothing is known to be its own),
// and a stamp that does not parse is not admitted either.
func after(ts string, t time.Time) bool {
	if t.IsZero() {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	return err == nil && !at.Before(t)
}

// fingerprint is a short stable name for s.
func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// listed names up to max of items, and how many more there are.
func listed(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:max], ", "), len(items)-max)
}
