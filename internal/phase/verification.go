package phase

import (
	"strings"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/journal"
)

// VerifyState is what is true of one commit's verification for one slice: a
// recorded result, or none. Only the journal's own lines decide it.
type VerifyState string

// The states of a commit's verification.
const (
	// VerifyNoResult: the journal holds no covering result line for the commit
	// and slice. An old verify-pending line is no result.
	VerifyNoResult VerifyState = "no recorded result"
	// VerifyCouldNotRun: the latest record is a verify-error line: the run
	// never started the oracle.
	VerifyCouldNotRun VerifyState = "could not run"
	// VerifyFailed: the latest covering oracle line is a failure. A run a
	// killed harness or a timeout ended is a recorded failed run that judged
	// nothing; CommitVerification.Cause says so.
	VerifyFailed VerifyState = "failed"
	// VerifyPass: the oracle passed on the commit.
	VerifyPass VerifyState = "pass"
)

// Unclear reports whether the state is one a reader should name: anything but
// a pass.
func (s VerifyState) Unclear() bool { return s != VerifyPass }

// Holds reports whether the state holds the builder at its stop: a commit with
// no recorded result, which the clearing command puts right. A failed or
// could-not-run one is told and lets the builder stop.
func (s VerifyState) Holds() bool { return s == VerifyNoResult }

// CommitRef is a commit of the ticket's branch and the slice its Jig-Slice
// trailer names. SHA is the full sha, the way the journal records it.
type CommitRef struct {
	SHA   string
	Slice string
}

// VerifyInput is everything DeriveVerifications reads. It holds no git,
// ledger or OS handle: the caller gathers the facts.
type VerifyInput struct {
	// Ticket names the ticket in the clearing commands.
	Ticket string
	// Lines are the ticket's journal lines.
	Lines []journal.Line
	// Commits are the branch's commits that carry a slice trailer, since the
	// ticket's start sha. When BranchKnown is false they are ignored and the
	// journal's own (commit, slice) pairs are judged instead, so a reader with
	// no clone still tells what the journal shows.
	Commits     []CommitRef
	BranchKnown bool
}

// CommitVerification is the truth about one commit's verification for a slice.
type CommitVerification struct {
	Slice string
	// Commit is the full sha.
	Commit string
	State  VerifyState
	// Clear is the command that clears the state, or the instruction when it
	// is fixed by a commit; "" for a pass.
	Clear string
	// FailLine is a failed run's first FAIL line, else the first non-blank
	// line of its summary; "" for any other state.
	FailLine string
	// Cause is the words of a failed run that judged nothing, "stopped, not
	// counted" or "timed out, not counted"; "" for any other state.
	Cause string
	// Error is a could-not-run verification's summary.
	Error   string
	Attempt int
}

// Told is the words the commit's state is told in beside its slice.
func (c CommitVerification) Told() string {
	if c.Cause != "" {
		return c.Cause
	}
	return string(c.State)
}

// DeriveVerifications judges each (commit, slice) of the branch from the
// journal: only the latest record of the pair counts, and each commit is
// judged on its own. The result follows the branch's commits in the order
// given (or the journal's, with no branch), one row per pair.
func DeriveVerifications(in VerifyInput) []CommitVerification {
	pairs := in.Commits
	if !in.BranchKnown {
		pairs = journalPairs(in.Lines)
	}
	seen := map[CommitRef]bool{}
	var out []CommitVerification
	for _, p := range pairs {
		if p.SHA == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, in.judge(p))
	}
	return out
}

// journalPairs are the (commit, slice) pairs the journal's verification lines
// name, in the order they first appear.
func journalPairs(lines []journal.Line) []CommitRef {
	var out []CommitRef
	seen := map[CommitRef]bool{}
	for _, l := range lines {
		if l.Commit == "" || l.Slice == "" {
			continue
		}
		if v := journal.VerificationState([]journal.Line{l}, l.Commit, l.Slice); v.State == journal.VerifyNone {
			continue
		}
		p := CommitRef{l.Commit, l.Slice}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (in VerifyInput) judge(p CommitRef) CommitVerification {
	v := journal.VerificationState(in.Lines, p.SHA, p.Slice)
	row := CommitVerification{Slice: p.Slice, Commit: p.SHA, Attempt: v.Attempt}
	rerun := "jig verify " + in.Ticket + " --slice " + p.Slice
	switch v.State {
	case journal.VerifyNone:
		row.State, row.Clear = VerifyNoResult, rerun
	case journal.VerifyPass:
		row.State = VerifyPass
	case journal.VerifyError:
		row.State, row.Clear, row.Error = VerifyCouldNotRun, rerun, FailLine(v.Summary)
	case journal.VerifyFail:
		row.State = VerifyFailed
		row.FailLine = FailLine(v.Summary)
		if row.Cause = v.NotCounted(); row.Cause != "" {
			row.Clear = rerun
		} else {
			row.Clear = "fix it and commit with the trailer Jig-Slice: " + p.Slice
		}
	}
	return row
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// failLineMax bounds a told failure line.
const failLineMax = 160

// FailLine picks the line that says what failed from a failed run's summary:
// the first line that starts with FAIL or "--- FAIL", else the first non-blank
// line. The journal flattens a summary to one line, so a flattened summary is
// searched for the first such marker at a word start and cut there.
func FailLine(summary string) string {
	lines := strings.Split(summary, "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "FAIL") || strings.HasPrefix(t, "--- FAIL") {
			return clip(t)
		}
	}
	if len(lines) == 1 {
		best := -1
		for _, m := range []string{"--- FAIL", "FAIL"} {
			for i := 0; i < len(summary); {
				j := strings.Index(summary[i:], m)
				if j < 0 {
					break
				}
				j += i
				if j == 0 || summary[j-1] == ' ' {
					if best < 0 || j < best {
						best = j
					}
					break
				}
				i = j + len(m)
			}
		}
		if best >= 0 {
			return clip(summary[best:])
		}
	}
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return clip(t)
		}
	}
	return ""
}

func clip(s string) string {
	if len(s) <= failLineMax {
		return s
	}
	s = s[:failLineMax]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
