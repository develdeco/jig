package verifydeliver

import (
	"github.com/develdeco/jig/internal/screen"
	"github.com/develdeco/jig/internal/session"
)

// sessionLauncher is the Launcher the tests of a launched round use: it runs
// the reviewer session on a session backend (a scenario player, a stub), the
// way internal/revieweval's launcher runs it on a real one. The two map the
// Launch to a session.Dispatch alike; internal/revieweval's test pins that
// side, and this copy exists because revieweval imports this package.
type sessionLauncher struct{ backend session.Backend }

func (s sessionLauncher) Launch(l Launch) error {
	return s.backend.Run(session.Dispatch{
		Ticket:     l.Ticket,
		Slice:      "gate",
		Attempt:    l.Round,
		Worktree:   l.Worktree,
		SliceJSON:  l.RequestPath,
		ResultJSON: l.ResultPath,
		Model:      l.Model,
		Effort:     l.Effort,
		Prompt:     l.Prompt,
		// The prompt names recordings.json (when there is one); a backend that
		// spells paths for its session spells that mention too.
		ExtraReadFile: l.RecordingsPath,
		Screen:        true,
	})
}

// Readable is what the screen that governs a headless reviewer's reads says of
// file: the same decision the session's hook makes.
func (s sessionLauncher) Readable(file string) bool {
	_, ok := screen.ToolCall("Read", map[string]any{"file_path": file})
	return ok
}

// newReviewerGateSource is the launched gate source on a session backend.
func newReviewerGateSource(b session.Backend) GateSource {
	return NewLaunchedGateSource(sessionLauncher{backend: b})
}
