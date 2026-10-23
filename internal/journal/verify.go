package journal

// What the journal says about `jig verify`: whether a commit has been
// verified for a slice, which verifications are still running, and which
// attempt the next one is. The lines are the frontier's own oracle and
// verified lines and the digester's verify-pending and verify-error lines
// (events.go); nothing here adds a line shape.

// The state of one (commit, slice) verification, as VerificationState reads
// it from the journal.
const (
	// VerifyNone: the journal has no record of verifying the commit for the
	// slice.
	VerifyNone = ""
	// VerifyPending: a verify-pending line is the latest record: a worker
	// started and has not recorded an outcome. It stays so if the worker
	// died; the journal cannot tell, and a rerun replaces it.
	VerifyPending = "pending"
	// VerifyPass: the oracle passed on the commit.
	VerifyPass = "pass"
	// VerifyFail: the oracle failed on the commit.
	VerifyFail = "fail"
	// VerifyError: the verification could not run the oracle (no worktree,
	// no oracle, an env class that would not come up); the oracle neither
	// passed nor failed.
	VerifyError = "error"
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
	// Summary is the failure's flattened tail for a fail, or the reason for
	// an error.
	Summary string
	// Session and Agent are who asked for the verification.
	Session string
	Agent   string
}

// verificationLine reports the state a line puts its (commit, slice) in, and
// false for a line that says nothing about one. A verified line is a pass on
// its own, since a journal that kept its verified line and lost its oracle
// line is still a verified commit (BuiltCommits reads it); an oracle line of
// either outcome counts, whoever ran it, because the frontier's lines are
// the evidence a commit was verified before the digester existed.
func verificationLine(l Line) (state string, ok bool) {
	switch l.Event {
	case EventVerifyPending:
		return VerifyPending, true
	case EventVerified:
		return VerifyPass, true
	case EventVerifyError:
		return VerifyError, true
	case EventOracle:
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
// slice from lines: the last of the slice's verify-pending, oracle, verified
// and verify-error lines at that commit, in journal order. A rerun therefore
// replaces an earlier outcome with its own pending line, then its own
// outcome. A recorded line says nothing about verification and is skipped.
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
		next := Verification{State: state, Attempt: l.Attempt, TS: l.TS, Session: l.Session, Agent: l.Agent}
		switch {
		case l.Event == EventVerified && v.State == VerifyPass && v.Attempt == l.Attempt:
			// The verified line that follows its own attempt's oracle line
			// carries no time of its own: keep the oracle line's.
			next.Seconds = v.Seconds
		default:
			next.Seconds, next.Summary = l.Seconds, l.Summary
		}
		v = next
	}
	return v
}

// PendingVerification is a verification the journal shows started and not
// finished.
type PendingVerification struct {
	Slice   string
	Commit  string
	Attempt int
	// TS is when the verify-pending line was written.
	TS      string
	Session string
	Agent   string
}

// Pending lists the verifications lines show started and not finished: each
// (commit, slice) whose latest record is a verify-pending line, in the order
// those lines were written. It cannot tell a running worker from one that
// died; a reader that needs to know checks the process the ledger recorded.
func Pending(lines []Line) []PendingVerification {
	type key struct{ slice, commit string }
	latest := map[key]int{} // the index of the (commit, slice)'s latest record
	for i, l := range lines {
		if l.Commit == "" {
			continue
		}
		if _, ok := verificationLine(l); ok {
			latest[key{l.Slice, l.Commit}] = i
		}
	}
	var out []PendingVerification
	for i, l := range lines {
		if l.Event != EventVerifyPending || l.Commit == "" || latest[key{l.Slice, l.Commit}] != i {
			continue
		}
		out = append(out, PendingVerification{Slice: l.Slice, Commit: l.Commit, Attempt: l.Attempt, TS: l.TS, Session: l.Session, Agent: l.Agent})
	}
	return out
}

// NextAttempt is the attempt number the next verification of slice takes: one
// more than the highest attempt any line of the slice carries, the frontier's
// dispatch lines and earlier verifications alike, so the numbering is one
// sequence per slice and a rerun of the same commit is a new attempt.
func NextAttempt(lines []Line, slice string) int {
	highest := 0
	for _, l := range lines {
		if l.Slice == slice && l.Attempt > highest {
			highest = l.Attempt
		}
	}
	return highest + 1
}
