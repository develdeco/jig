package revieweval

import (
	"github.com/develdeco/jig/internal/screen"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The launched reviewer: the one place jig's code starts a reviewer session.
// The jig binary does not (a round it opens is reviewed by an agent it did not
// start: GatePrepare, GateFinish), but measuring a reviewer needs one run on
// demand, so the evaluation launches it here. Everything about a launched
// round that does not need a session - the request it writes
// (PrepareReviewRequest), the prompt, the check of what the session wrote
// (the same check GateFinish applies to a recorded reviewer's report) - lives
// in verifydeliver's launched gate source; this file is the part that does.

// launcher runs a reviewer session on a session backend, screened.
type launcher struct{ backend session.Backend }

// reviewerSource returns the gate source that runs every round's reviewer on
// backend: the code path Gate itself ran for a real reviewer before the gate
// was recorded, so the evaluation measures the reviewer contract as it is.
func reviewerSource(backend session.Backend) verifydeliver.GateSource {
	return verifydeliver.NewLaunchedGateSource(launcher{backend})
}

// Launch runs the reviewer as a screened session in the gate lease: it reads
// the round's request, writes its result, and is handed the one file outside
// the lease the prompt names (the build's recordings list) as an extra read.
func (l launcher) Launch(v verifydeliver.Launch) error {
	return l.backend.Run(session.Dispatch{
		Ticket:     v.Ticket,
		Slice:      "gate",
		Attempt:    v.Round,
		Worktree:   v.Worktree,
		SliceJSON:  v.RequestPath,
		ResultJSON: v.ResultPath,
		Model:      v.Model,
		Effort:     v.Effort,
		Prompt:     v.Prompt,
		// The prompt names recordings.json (when there is one); a backend that
		// spells paths for its session spells that mention too.
		ExtraReadFile: v.RecordingsPath,
		Screen:        true,
	})
}

// Readable is whether the screen that governs the session's reads lets it
// Read file: the same decision the session's hook makes.
func (l launcher) Readable(file string) bool {
	_, ok := screen.ToolCall("Read", map[string]any{"file_path": file})
	return ok
}
