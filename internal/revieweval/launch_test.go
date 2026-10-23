package revieweval

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// recordingBackend keeps the dispatches it is run with.
type recordingBackend struct{ got []session.Dispatch }

func (b *recordingBackend) Run(d session.Dispatch) error {
	b.got = append(b.got, d)
	return nil
}

// TestLauncherRunsTheReviewerAsAScreenedGateSession: what a launched round
// hands its reviewer reaches the session backend as a screened dispatch in the
// gate lease, reading the request and writing the result, with the recordings
// list as the one extra file to read. verifydeliver's tests run the same
// mapping on their own copy (they cannot import this package), so this pins
// the real one.
func TestLauncherRunsTheReviewerAsAScreenedGateSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := verifydeliver.Launch{
		Ticket: "T-1", Round: 3, Worktree: filepath.Join(dir, "lease"),
		RequestPath: filepath.Join(dir, "review.json"), ResultPath: filepath.Join(dir, "result.json"),
		RecordingsPath: filepath.Join(dir, "recordings.json"),
		Model:          "claude-test", Effort: "high", Prompt: "review round 3",
	}
	backend := &recordingBackend{}
	if err := (launcher{backend}).Launch(v); err != nil {
		t.Fatal(err)
	}
	if len(backend.got) != 1 {
		t.Fatalf("the backend ran %d dispatches, want 1", len(backend.got))
	}
	want := session.Dispatch{
		Ticket: "T-1", Slice: "gate", Attempt: 3, Worktree: v.Worktree,
		SliceJSON: v.RequestPath, ResultJSON: v.ResultPath, ExtraReadFile: v.RecordingsPath,
		Model: "claude-test", Effort: "high", Prompt: "review round 3", Screen: true,
	}
	if backend.got[0] != want {
		t.Errorf("dispatch = %+v, want %+v", backend.got[0], want)
	}
}

// TestLauncherReadsAreThoseOfTheScreen: a recording the screen would refuse to
// let the reviewer read is not one the launched round lists for it.
func TestLauncherReadsAreThoseOfTheScreen(t *testing.T) {
	t.Parallel()
	l := launcher{&recordingBackend{}}
	if !l.Readable(filepath.Join(t.TempDir(), "home.svg")) {
		t.Error("a plain recording is not readable")
	}
	if l.Readable(filepath.Join(t.TempDir(), ".env-setup.svg")) {
		t.Error("a credential-named recording is readable, but the screen refuses it")
	}
}
