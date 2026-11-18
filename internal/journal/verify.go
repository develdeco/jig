package journal

// What the journal says about `jig verify`: whether a commit has a recorded
// result for a slice, and which attempt the next one is. The lines are the
// frontier's own oracle and verified lines and the digester's verify-error
// line (events.go); nothing here adds a line shape. A verify-pending line is
// an old worker's, no result, and is skipped.

// The state of one (commit, slice) verification, as VerificationState reads
// it from the journal.
const (
	// VerifyNone: the journal has no record of verifying the commit for the
	// slice.
	VerifyNone = ""
	// VerifyPass: the oracle passed on the commit.
	VerifyPass = "pass"
	// VerifyFail: the oracle failed on the commit.
	VerifyFail = "fail"
	// VerifyError: the verification could not run the oracle (no worktree,
	// no oracle, an env class that would not come up); the oracle neither
	// passed nor failed.
	VerifyError = "error"
)

// The states of a working tree an oracle line records (Line.TreeBefore and
// TreeAfter).
const (
	TreeClean = "clean"
	TreeDirty = "dirty"
)

// Why a run of `jig verify` covers no commit (Line.Uncovered). A line that
// carries one is recorded and read by no reader of verification state.
const (
	UncoveredDirtyBefore = "tree dirty before"
	UncoveredDirtyAfter  = "tree dirty after"
	UncoveredHeadMoved   = "head moved"
	UncoveredOtherCmd    = "another command"
	UncoveredNotDescend  = "not a descendant of the start sha"
	UncoveredOtherDir    = "another directory"
)

// Verification is the latest record of verifying one commit for one slice.
type Verification struct {
	// State is one of the Verify constants.
	State string
	// Attempt is the slice attempt the latest record belongs to.
	Attempt int
	// TS is when the latest record was written.
	TS string
	// Seconds is the oracle's wall time, for a pass or fail.
	Seconds int
	// CPUMs, PeakBytes and PeakBasis are what the oracle's process tree used
	// (Line.CPUMs): 0 and "" when the line carries none.
	CPUMs     int64
	PeakBytes uint64
	PeakBasis string
	// Summary is the failure's flattened tail for a fail, or the reason for
	// an error.
	Summary string
	// Session and Agent are who asked for the verification.
	Session string
	Agent   string
	// Ran and ReusedFrom are set on a pass whose delta was light: the light
	// command that ran in the place of the slice's oracle, and the commit of
	// the full run the pass stands on.
	Ran, ReusedFrom string
	// Cause is a failed run's cause (Line.Cause): set when the run never
	// judged the code.
	Cause string
}

// NotCounted says how a failed run stood when it never judged the code:
// "timed out, not counted" or "stopped, not counted", and "" for any other
// verification.
func (v Verification) NotCounted() string {
	if v.State != VerifyFail {
		return ""
	}
	switch v.Cause {
	case CauseTimeout:
		return "timed out, not counted"
	case CauseStopped:
		return "stopped, not counted"
	}
	return ""
}

// LightWords says how a light pass stood: "lint only, reusing <sha>" with the
// origin run's sha shortened to 12, and "" for a verification that was not a
// light pass.
func (v Verification) LightWords() string {
	if v.State != VerifyPass || v.Ran == "" || v.ReusedFrom == "" {
		return ""
	}
	origin := v.ReusedFrom
	if len(origin) > 12 {
		origin = origin[:12]
	}
	return "lint only, reusing " + origin
}

// verificationLine reports the state a line puts its (commit, slice) in, and
// false for a line that says nothing about one. A verified line is a pass on
// its own, since a journal that kept its verified line and lost its oracle
// line is still a verified commit (BuiltCommits reads it); an oracle line of
// either outcome counts, whoever ran it, because the frontier's lines are
// the evidence a commit was verified before the digester existed. A
// verify-pending line is no result: an old worker wrote it, and it hides
// nothing a run before it recorded.
func verificationLine(l Line) (state string, ok bool) {
	switch l.Event {
	case EventVerified:
		return VerifyPass, true
	case EventVerifyError:
		return VerifyError, true
	case EventOracle:
		if l.Uncovered != "" {
			// A run that covers no commit: recorded, and says nothing of one.
			return "", false
		}
		switch l.Outcome {
		case "pass":
			return VerifyPass, true
		case "fail":
			return VerifyFail, true
		}
	}
	return "", false
}

// VerificationState reads the latest record of verifying commit sha for
// slice from lines: the last of the slice's covering oracle, verified and
// verify-error lines at that commit, in journal order. A line that records no
// result (a verify-pending line, an uncovered run) is skipped.
func VerificationState(lines []Line, sha, slice string) Verification {
	var v Verification
	if sha == "" {
		return v
	}
	for _, l := range lines {
		if l.Slice != slice || l.Commit != sha {
			continue
		}
		state, ok := verificationLine(l)
		if !ok {
			continue
		}
		next := Verification{State: state, Attempt: l.Attempt, TS: l.TS, Session: l.Session, Agent: l.Agent, Ran: l.Ran, ReusedFrom: l.ReusedFrom, Cause: l.Cause}
		switch {
		case l.Event == EventVerified && v.State == VerifyPass && v.Attempt == l.Attempt:
			// The verified line that follows its own attempt's oracle line
			// carries no time of its own: keep the oracle line's.
			next.Seconds, next.Ran, next.ReusedFrom = v.Seconds, v.Ran, v.ReusedFrom
			next.CPUMs, next.PeakBytes, next.PeakBasis = v.CPUMs, v.PeakBytes, v.PeakBasis
		default:
			next.Seconds, next.Summary = l.Seconds, l.Summary
			next.CPUMs, next.PeakBytes, next.PeakBasis = l.CPUMs, l.PeakBytes, l.PeakBasis
		}
		v = next
	}
	return v
}

// NextAttempt is the attempt number the next verification of slice takes: one
// more than the highest attempt any line of the slice carries, the frontier's
// dispatch lines and earlier verifications alike, so the numbering is one
// sequence per slice and a rerun of the same commit is a new attempt. A
// fix-slice line is not an attempt: older journals carry the gate round on it,
// and a fix slice's first verification is attempt 1 all the same.
func NextAttempt(lines []Line, slice string) int {
	highest := 0
	for _, l := range lines {
		if l.Slice == slice && l.Event != EventFixSlice && l.Attempt > highest {
			highest = l.Attempt
		}
	}
	return highest + 1
}

// FinalRunAt is the final run `jig verify --final` recorded at exactly head
// for repo: the latest final-oracle line of that repo whose commit is head,
// that covers it (no Uncovered reason) and judged the code (a pass or a fail,
// not a run jig's stop ended). repo is "" in a project of one repo, whose
// lines carry none. A run at another commit stands for nothing, even one with
// the same tree: nothing borrows a pass. ok is false when there is none.
func FinalRunAt(lines []Line, repo, head string) (line Line, ok bool) {
	if head == "" {
		return Line{}, false
	}
	for _, l := range lines {
		if l.Event != EventFinalOracle || l.Commit != head || l.Repo != repo || l.Uncovered != "" {
			continue
		}
		if l.Outcome != "pass" && l.Outcome != "fail" {
			continue
		}
		if l.Cause == CauseStopped {
			continue
		}
		line, ok = l, true
	}
	return line, ok
}
