package journal

// The event names a Line carries in Event. A name is a constant here so a
// reader and a writer spell it once; a call site that still writes the
// literal means the same thing.
//
// A name is either a journal event (a ticket's journal.ndjson, in the
// store, shared by every machine) or a ledger-only event (the machine
// ledger under the jig home, which no other machine sees), or both: a
// writer picks the file, the name is the same.

// Events jig's dispatch loop, gate and publish already write to a ticket's
// journal.
const (
	EventAdopt          = "adopt"
	EventAnswer         = "answer"
	EventCall           = "call"
	EventChangelog      = "changelog"
	EventDispatch       = "dispatch"
	EventEnvDown        = "env-down"
	EventEnvUnavailable = "env-unavailable"
	EventEnvUp          = "env-up"
	EventFinalOracle    = "final-oracle"
	EventFixSlice       = "fix-slice"
	EventGateClean      = "gate-clean"
	EventGateOracle     = "gate-oracle"
	EventGateOpen       = "gate-open"
	EventGateReviewer   = "gate-reviewer"
	EventGateRound      = "gate-round"
	EventLeaseRecovered = "lease-recovered"
	EventMemorize       = "memorize"
	EventOracle         = "oracle"
	EventPr             = "pr"
	EventPublishDone    = "publish-done"
	EventPublishPicks   = "publish-picks"
	EventQuestion       = "question"
	EventReconcile      = "reconcile"
	EventRecorded       = "recorded"
	EventRequeue        = "requeue"
	EventRestamp        = "restamp"
	EventResult         = "result"
	EventReviewNotes    = "review-notes"
	EventRevalidate     = "revalidate"
	EventRoute          = "route"
	EventSliceAdd       = "slice-add"
	EventSquash         = "squash"
	EventStall          = "stall"
	EventVerified       = "verified"
)

// Events the digester adds to a ticket's journal.
const (
	EventVerifyPending   = "verify-pending"
	EventVerifyError     = "verify-error"
	EventClaim           = "claim"
	EventRelease         = "release"
	EventDriver          = "driver"
	EventNote            = "note"
	EventCorrection      = "correction"
	EventSpend           = "spend"
	EventApproval        = "approval"
	EventTriage          = "triage"
	EventKeepQueued      = "keep-queued"
	EventPrBody          = "pr-body"
	EventPublishApproved = "publish-approved"
	// EventLanded is the line a landing writes, one per repo of a ticket whose
	// pull request merged: the repo, the pull request (URL), the branch it merged
	// into (Base), the container it went into (Into), the merge commit (Commit)
	// and who made it known (Provenance).
	EventLanded = "landed"
	// EventSuiteWaived is the line `jig publish --without-suite` writes into an
	// issue's journal, one per repo whose suite is unmet: the repo, the tip it
	// waives (Commit), the reason (Summary) and who said it (Provenance). It
	// stands for that tip only (SuiteWaivedAt).
	EventSuiteWaived = "suite-waived"
)

// Events the machine ledger records and a ticket's journal does not.
const (
	EventSessionStart  = "session-start"
	EventSessionTouch  = "session-touch"
	EventSessionEnd    = "session-end"
	EventTool          = "tool"
	EventGitCommit     = "git-commit"
	EventVerifySpawned = "verify-spawned"
	EventNoteDelivered = "note-delivered"
	EventHook          = "hook"
	// EventLandNamed is the line `jig land` leaves in the ledger when it names a
	// merge for the session to make: the landed line that follows is then an
	// agent's or a person's, and without it a merged pull request is a merge
	// made on the host.
	EventLandNamed = "land-named"
)
