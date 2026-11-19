package verifydeliver

// A builder's probes (ADR 0036, amended): with no gate round open, the session
// that holds a slice records its mutation probes and skips as journal lines for
// that slice, each with the blob of its file in the builder's worktree. The
// next round reads them with the earlier rounds' probes, so a current, caught
// one covers its term and a survivor is probed again. Probes of one line rank
// by when they were made: a builder's probe after round N closed is later than
// round N's, and one made before it closed is earlier.

import (
	"fmt"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// CheckBuilderDraft holds a builder's probe or skip to the shape rules a
// round's draft is held to and returns it with its file normalized. A builder's
// draft is one probe or one skip, never a finding.
func CheckBuilderDraft(dr Draft) (Draft, error) {
	switch {
	case dr.kinds() != 1 || (dr.Probe == nil && dr.Skip == nil):
		return dr, reviewInvalid("a builder records one probe or one skip")
	case dr.Probe != nil:
		p := *dr.Probe
		if err := checkResultProbe(0, p); err != nil {
			return dr, err
		}
		p.File, _ = normalizeRepoRelPath(p.File)
		dr.Probe = &p
	default:
		s := *dr.Skip
		if err := checkResultSkip(0, s); err != nil {
			return dr, err
		}
		s.File, _ = normalizeRepoRelPath(s.File)
		dr.Skip = &s
	}
	return dr, nil
}

// BuilderLine is the journal line a builder's draft is recorded as, for slice,
// with blob the git blob of its file in the worktree and now the time it is
// made at.
func BuilderLine(slice, session, blob, now string, dr Draft) journal.Line {
	l := journal.Line{TS: now, Slice: slice, Event: journal.EventBuilderProbe, Session: session}
	if p := dr.Probe; p != nil {
		l.Probe = &journal.ProbeNote{
			Seam: p.Seam, File: p.File, Line: p.Line, Mutation: p.Mutation, Command: p.Command, Outcome: p.Outcome, Blob: blob,
		}
	}
	if s := dr.Skip; s != nil {
		l.Skip = &journal.SkipNote{File: s.File, From: s.From, To: s.To, Why: s.Why, Blob: blob}
	}
	return l
}

// BuilderFile is the file of a builder's draft as the round names it.
func BuilderFile(dr Draft) string {
	if dr.Probe != nil {
		return dr.Probe.File
	}
	return dr.Skip.File
}

// parseStamp reads a journal line's time; ok is false when it has none.
func parseStamp(ts string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	return t, err == nil
}

// builderLedger reads the builder-probe lines of ticket as ledger records.
// A probe's After is how many gate rounds had closed by the time it was made,
// from the closing lines' times, and At its own time.
func builderLedger(st *store.Store, ticket string) ([]ProbeRecord, []SkipRecord, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, nil, fmt.Errorf("verifydeliver: probes: read the journal: %w", err)
	}
	var closed []time.Time
	for _, l := range lines {
		if l.Event != journal.EventGateRound && l.Event != journal.EventGateClean {
			continue
		}
		if t, ok := parseStamp(l.TS); ok {
			closed = append(closed, t)
		}
	}
	var probes []ProbeRecord
	var skips []SkipRecord
	for _, l := range lines {
		if l.Event != journal.EventBuilderProbe {
			continue
		}
		at, ok := parseStamp(l.TS)
		if !ok {
			continue
		}
		after := 0
		for _, c := range closed {
			if !c.After(at) {
				after++
			}
		}
		switch {
		case l.Probe != nil:
			p := l.Probe
			probes = append(probes, ProbeRecord{
				Seam: p.Seam, File: p.File, Line: p.Line, Mutation: p.Mutation, Command: p.Command, Outcome: p.Outcome, Blob: p.Blob,
				Slice: l.Slice, After: after, At: at.UnixNano(),
			})
		case l.Skip != nil:
			s := l.Skip
			skips = append(skips, SkipRecord{File: s.File, From: s.From, To: s.To, Why: s.Why, Blob: s.Blob, Slice: l.Slice})
		}
	}
	return probes, skips, nil
}

// rankBefore reports whether probe a was made before probe b: by round for a
// round's probes, and a builder's probe sits between the round that had closed
// when it was made and the next, ordered by its own time among its peers.
func rankBefore(a, b ProbeRecord) bool {
	ka, kb := a.rankKey(), b.rankKey()
	if ka != kb {
		return ka < kb
	}
	return a.At < b.At
}

// rankKey is where a probe falls among the rounds: 2n for round n's own, and
// 2n+1 for a builder's made after n rounds had closed.
func (p ProbeRecord) rankKey() int {
	if p.Slice == "" {
		return 2 * p.Round
	}
	return 2*p.After + 1
}
