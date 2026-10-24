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
	EventChangelog      = "changelog"
	EventDispatch       = "dispatch"
	EventEnvDown        = "env-down"
	EventEnvUnavailable = "env-unavailable"
	EventEnvUp          = "env-up"
	EventFixSlice       = "fix-slice"
	EventGateClean      = "gate-clean"
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
	EventResult         = "result"
	EventRevalidate     = "revalidate"
	EventRoute          = "route"
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
	EventPrBody          = "pr-body"
	EventPublishApproved = "publish-approved"
)

// Events the machine ledger records and a ticket's journal does not.
// EventGraph is also what the frontier's graph oracle writes to a journal.
const (
	EventSessionStart  = "session-start"
	EventSessionTouch  = "session-touch"
	EventSessionEnd    = "session-end"
	EventTool          = "tool"
	EventGitCommit     = "git-commit"
	EventVerifySpawned = "verify-spawned"
	EventNoteDelivered = "note-delivered"
	EventHook          = "hook"
	EventGraph         = "graph"
)
