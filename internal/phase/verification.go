package phase

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/journal"
)

// VerifyState is what is true of one commit's verification for one slice.
type VerifyState string

// The states of a commit's verification.
const (
	// VerifyNotVerified: the commit is on the branch and the journal holds no
	// verification of it for the slice.
	VerifyNotVerified VerifyState = "not verified"
	// VerifyStale: the latest record is a verify-pending line whose worker is
	// gone.
	VerifyStale VerifyState = "stale"
	// VerifyStopped: the latest record is a failed oracle line with cause
	// stopped, or a verify-error line: the run never judged the code.
	VerifyStopped VerifyState = "stopped"
	// VerifyFailed: the latest record is a failed oracle line with no cause.
	VerifyFailed VerifyState = "failed"
	// VerifyTimedOut: the latest record is a failed oracle line the wedge
	// guard ended. It is told, not stood aside: running it again unasked would
	// repeat the wait.
	VerifyTimedOut VerifyState = "timed out"
	// VerifyRunning: the latest record is a verify-pending line and its worker
	// is alive (or, with no spawned line to ask, young enough to be).
	VerifyRunning VerifyState = "running"
	// VerifyPass: the oracle passed on the commit.
	VerifyPass VerifyState = "pass"
)

// Told is the words a state is told in beside its slice.
func (s VerifyState) Told() string {
	switch s {
	case VerifyStopped:
		return "stopped, not counted"
	case VerifyTimedOut:
		return "timed out, not counted"
	case VerifyStale:
		return "stale: worker gone"
	}
	return string(s)
}

// StandsAside reports whether a request to verify the commit runs the oracle
// without being forced: the verification is stale or stopped, and nothing in
// it judged the code.
func (s VerifyState) StandsAside() bool { return s == VerifyStale || s == VerifyStopped }

// Unclear reports whether the state is one a reader should name: anything but
// a pass or a verification under way.
func (s VerifyState) Unclear() bool { return s != VerifyPass && s != VerifyRunning }

// Holds reports whether the state holds the builder at its stop: a commit no
// one is judging, which the clearing command puts right. A failed or stopped
// one is told and lets the builder stop.
func (s VerifyState) Holds() bool { return s == VerifyNotVerified || s == VerifyStale }

// PendingMaxAge is how old a verify-pending line with no spawned line may be
// and still count as running: a worker the ledger never saw is not called
// stale on sight.
const PendingMaxAge = 2 * time.Hour

// CommitRef is a commit of the ticket's branch and the slice its Jig-Slice
// trailer names. SHA is the full sha, the way the journal records it.
type CommitRef struct {
	SHA   string
	Slice string
}

// Spawned is what the ledger's spawned line says of a verification's worker:
// its process id and when the line was written, a little after the process
// started.
type Spawned struct {
	PID int
	At  time.Time
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
	// Spawned are the spawned lines of each (commit, slice) the journal shows
	// pending, any order. Only one that started the pending line's worker
	// counts (spawnTie).
	Spawned map[CommitRef][]Spawned
	Now     time.Time
	// Alive says whether a process with the pid exists and was created no
	// later than the time; nil counts every worker alive.
	Alive func(pid int, notBefore time.Time) bool
}

// CommitVerification is the truth about one commit's verification for a slice.
type CommitVerification struct {
	Slice string
	// Commit is the full sha.
	Commit string
	State  VerifyState
	// Clear is the command that clears the state, or the instruction when it
	// is fixed by a commit; "" for a pass or a running verification.
	Clear string
	// FailLine is a failed run's first FAIL line, else the first non-blank
	// line of its summary; "" for any other state.
	FailLine string
	Attempt  int
}

// DeriveVerifications judges each (commit, slice) of the branch from the
// journal: only the latest record of the pair counts, and each commit is
// judged on its own. The result follows the branch's commits in the order
// given (or the journal's, with no branch), one row per pair.
func DeriveVerifications(in VerifyInput) []CommitVerification {
	pairs := in.Commits
	if !in.BranchKnown {
		pairs = journalPairs(in.Lines)
	} else {
		// A verification the journal shows under way is judged whether or not
		// its commit is on the branch read: the worker is running either way.
		pairs = append(append([]CommitRef(nil), pairs...), pendingPairs(in.Lines)...)
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
	rerun := "jig verify " + in.Ticket + " " + shortSHA(p.SHA) + " --slice " + p.Slice + " --wait"
	switch v.State {
	case journal.VerifyNone:
		row.State, row.Clear = VerifyNotVerified, rerun
	case journal.VerifyPass:
		row.State = VerifyPass
	case journal.VerifyError:
		row.State, row.Clear = VerifyStopped, rerun
	case journal.VerifyFail:
		switch v.Cause {
		case journal.CauseStopped:
			row.State, row.Clear = VerifyStopped, rerun
		case journal.CauseTimeout:
			row.State, row.Clear = VerifyTimedOut, rerun+" --rerun"
		default:
			row.State, row.Clear = VerifyFailed, "fix it and commit with the trailer Jig-Slice: "+p.Slice
			row.FailLine = FailLine(v.Summary)
		}
	case journal.VerifyPending:
		if in.workerGone(p, v) {
			row.State, row.Clear = VerifyStale, rerun
		} else {
			row.State = VerifyRunning
		}
	}
	return row
}

// workerGone reports whether the pending verification v has no live worker:
// the spawned line's process is gone or was created after the line, and with
// no spawned line, the pending line is older than PendingMaxAge.
func (in VerifyInput) workerGone(p CommitRef, v journal.Verification) bool {
	ts, err := time.Parse(time.RFC3339Nano, v.TS)
	if err != nil {
		return false
	}
	// The latest spawned line that started this pending line's worker. A
	// worker that ran in process (jig verify --wait) leaves none, and a line
	// of an earlier or later run is not its worker: the age rule judges it.
	var tied *Spawned
	for i, sp := range in.Spawned[p] {
		if sp.PID > 0 && spawnTie(sp.At, ts) && (tied == nil || sp.At.After(tied.At)) {
			tied = &in.Spawned[p][i]
		}
	}
	if tied != nil {
		return in.Alive != nil && !in.Alive(tied.PID, tied.At)
	}
	return in.Now.Sub(ts) > PendingMaxAge
}

// spawnTie says whether a spawned line written at spawned started the worker
// that wrote the pending line at pending: the line is written right after the
// process starts and the worker writes its pending line first thing, so the
// two are close, the spawned line usually the earlier. A line far from the
// pending line belongs to another run of the same commit.
func spawnTie(spawned, pending time.Time) bool {
	gap := pending.Sub(spawned)
	return gap >= -spawnLead && gap <= spawnLag
}

const (
	// spawnLead is how long after the pending line its worker's spawned line
	// may be written; spawnLag how long before it.
	spawnLead = 10 * time.Second
	spawnLag  = 5 * time.Minute
)

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

// pendingPairs are the (commit, slice) pairs whose latest journal record is a
// verify-pending line.
func pendingPairs(lines []journal.Line) []CommitRef {
	var out []CommitRef
	for _, p := range journal.Pending(lines) {
		out = append(out, CommitRef{p.Commit, p.Slice})
	}
	return out
}
