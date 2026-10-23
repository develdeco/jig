package verifydeliver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// --- intentExcerpt -----------------------------------------------------------

// newIntentInferLease builds a minimal git repo standing in for a gate
// lease, suitable for intentExcerpt's own structural steps: one commit on
// target, pinned to the fixed date the rest of this package's own tests
// use (reviewGitEnv), with a refs/remotes/origin/<target> ref pointing at
// it so gitx.MergeBase resolves. Returns the lease dir and its HEAD sha.
func newIntentInferLease(t *testing.T) (dir, head string) {
	t.Helper()
	dir = newReviewLease(t, "main")
	sha, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return dir, sha
}

// newIntentInferHomes returns two fresh temp dirs: an operator home (where
// a test writes synthetic transcripts, RoundInput.UserHome) and a jig home
// root (RoundInput.Home). Neither touches the process environment, so a
// test using them runs in parallel and never reaches the real home
// directory.
func newIntentInferHomes(t *testing.T) (userHome, jigHome string) {
	t.Helper()
	return t.TempDir(), t.TempDir()
}

// intentInferSessionMTime is inside the [baseTime-3d, headTime+1h] window
// newIntentInferLease's single, fixed-date commit produces (base and head
// both resolve to reviewGitEnv's 2026-01-01T00:00:00Z), matching the
// window intent_infer_e2e_test.go's own fixture-based tests rely on.
var intentInferSessionMTime = time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)

// assertNoExcerpt fails unless intentExcerpt gave no path and exactly note.
func assertNoExcerpt(t *testing.T, path, note, want string) {
	t.Helper()
	if path != "" {
		t.Fatalf("intentExcerpt path = %q, want none", path)
	}
	if note != want {
		t.Fatalf("intentExcerpt note = %q, want %q", note, want)
	}
}

// assertNoExcerptWithPrefix is assertNoExcerpt for a note that ends in the
// text of a real cause: it must start with prefix and carry something after.
func assertNoExcerptWithPrefix(t *testing.T, path, note, prefix string) {
	t.Helper()
	if path != "" {
		t.Fatalf("intentExcerpt path = %q, want none", path)
	}
	if !strings.HasPrefix(note, prefix) || strings.TrimPrefix(note, prefix) == "" {
		t.Fatalf("intentExcerpt note = %q, want it to start with %q and carry the cause", note, prefix)
	}
}

func TestExcerptFailedNamesTheCause(t *testing.T) {
	t.Parallel()
	if got, want := excerptFailed("could not read the thing", os.ErrPermission), "could not read the thing: "+os.ErrPermission.Error(); got != want {
		t.Fatalf("excerptFailed = %q, want %q", got, want)
	}
}

func TestIntentExcerptNoOperatorClone(t *testing.T) {
	t.Parallel()
	dir, head := newIntentInferLease(t)
	in := RoundInput{Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir, RepoName: "fixture-repo", Target: "main"}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerpt(t, path, note, "no mapped clone recorded for this repo")
}

func TestIntentExcerptBadOperatorClone(t *testing.T) {
	t.Parallel()
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: t.TempDir(), // not a git repo
	}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerptWithPrefix(t, path, note, "could not resolve the operator's clone identity: ")
}

func TestIntentExcerptBadTargetNoMergeBase(t *testing.T) {
	t.Parallel()
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "does-not-exist", OperatorClone: dir,
	}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerptWithPrefix(t, path, note, "could not resolve the ticket's merge base: ")
}

func TestIntentExcerptNoLocalSessions(t *testing.T) {
	t.Parallel()
	userHome, jigHome := newIntentInferHomes(t) // empty: no .claude/projects at all
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: jigHome, UserHome: userHome,
	}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerpt(t, path, note, "no local agent sessions found for this repo and window")
}

// TestIntentExcerptNoUserHome: with no operator home to look in, the search
// says so and never falls back to searching anywhere else (the process's own
// home, the working directory).
func TestIntentExcerptNoUserHome(t *testing.T) {
	t.Parallel()
	_, jigHome := newIntentInferHomes(t)
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: jigHome,
	}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerpt(t, path, note, "no home directory to look for local agent sessions in")
}

// TestIntentExcerptNoJigHome: with no jig home root, the excerpt has nowhere
// to be written, so the search stops there instead of writing it relative to
// the working directory.
func TestIntentExcerptNoJigHome(t *testing.T) {
	t.Parallel()
	userHome, _ := newIntentInferHomes(t)
	dir, head := newIntentInferLease(t)
	diffFiles := []string{"alpha/alpha.go", "alpha/percent.go"}
	writeSyntheticClaudeSession(t, userHome, "session-match", dir, diffFiles, intentInferSessionMTime)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		UserHome: userHome,
	}

	path, note := intentExcerpt(in, head, diffFiles)
	assertNoExcerptWithPrefix(t, path, note, "could not write the session excerpt: ")
}

// TestIntentExcerptDiscoverErrorNamesCause: the "no local agent sessions
// found" note must not swallow a real Discover error: a genuine failure
// (here, <home>/.claude/projects existing as a plain file, not a
// directory, so os.ReadDir fails with something other than "not exist")
// must surface its own cause, distinct from the ordinary "nothing found"
// case above. Windows-only skip: opening a plain file where a directory is
// expected surfaces as ERROR_PATH_NOT_FOUND there, which os.IsNotExist itself
// reports true for - Discover's own "nothing to find" branch, not a real
// error - so this specific probe cannot distinguish the two failure modes on
// that OS.
func TestIntentExcerptDiscoverErrorNamesCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.ReadDir(file-as-dir) reports os.IsNotExist true on Windows, not a distinguishable error")
	}
	t.Parallel()
	homeDir, jigHome := newIntentInferHomes(t)
	if err := os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file where Discover expects a directory to list.
	if err := os.WriteFile(filepath.Join(homeDir, ".claude", "projects"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: jigHome, UserHome: homeDir,
	}

	path, note := intentExcerpt(in, head, []string{"a.go"})
	assertNoExcerptWithPrefix(t, path, note, "could not discover local agent sessions: ")
}

func TestIntentExcerptBelowMatchThreshold(t *testing.T) {
	t.Parallel()
	homeDir, jigHome := newIntentInferHomes(t)
	dir, head := newIntentInferLease(t)
	writeSyntheticClaudeSession(t, homeDir, "session-unrelated", dir, []string{"unrelated/file.go"}, intentInferSessionMTime)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: jigHome, UserHome: homeDir,
	}

	path, note := intentExcerpt(in, head, []string{"alpha/alpha.go", "alpha/percent.go"})
	assertNoExcerpt(t, path, note, "no session's file overlap cleared the match threshold")
}

// TestIntentExcerptCannotWriteExcerpt hands the search a jig home root that is
// a plain file instead of a directory (the excerpt directory is under the
// jig home, distinct from the operator home a session is discovered
// under), so writing the matched session's excerpt fails at the MkdirAll
// step.
func TestIntentExcerptCannotWriteExcerpt(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	notADir := filepath.Join(t.TempDir(), "jig-home-is-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, head := newIntentInferLease(t)
	diffFiles := []string{"alpha/alpha.go", "alpha/percent.go"}
	writeSyntheticClaudeSession(t, homeDir, "session-match", dir, diffFiles, intentInferSessionMTime)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: notADir, UserHome: homeDir,
	}

	path, note := intentExcerpt(in, head, diffFiles)
	assertNoExcerptWithPrefix(t, path, note, "could not write the session excerpt: ")
}

// TestIntentExcerptWritesTheMatchingSessionUnderTheJigHome: a session that
// mentions the diff's files is excerpted into a file of its own under the jig
// home, owner-only, holding what the author said; nothing else is written and
// no session is started. The path is unique per ticket and session.
func TestIntentExcerptWritesTheMatchingSessionUnderTheJigHome(t *testing.T) {
	t.Parallel()
	homeDir, jigHome := newIntentInferHomes(t)
	dir, head := newIntentInferLease(t)
	diffFiles := []string{"alpha/alpha.go", "alpha/percent.go"}
	writeSyntheticClaudeSession(t, homeDir, "session-match", dir, diffFiles, intentInferSessionMTime)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
		Home: jigHome, UserHome: homeDir,
	}

	path, note := intentExcerpt(in, head, diffFiles)
	if note != "" {
		t.Fatalf("intentExcerpt note = %q, want none", note)
	}
	if want := filepath.Join(home.IntentExcerptDir(jigHome), "JIG-1", "session-match.md"); path != want {
		t.Fatalf("intentExcerpt path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), intentInferExcerptMarker) {
		t.Fatalf("the excerpt = %q (%v), want the transcript's words", data, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the excerpt's mode = %v (%v), want owner-only 0600: a transcript can hold secrets", info.Mode(), err)
		}
	}
	if _, err := os.Stat(in.Store.IntentPath("JIG-1")); err == nil {
		t.Errorf("an intent.md was written, but jig summarizes nothing")
	}
}

// --- PrepareReviewRequest ----------------------------------------------------

// TestPrepareReviewRequestPointsAtTheExcerptOnlyWhenNoIntentIsStated: with
// nothing stating the intent, review.json names the excerpt of the matching
// session; with a stated intent it names none, and the session is not even
// looked for.
func TestPrepareReviewRequestPointsAtTheExcerptOnlyWhenNoIntentIsStated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		intent Intent
		want   bool
	}{
		{"none", Intent{Source: IntentSourceNone}, true},
		{"brief", Intent{Source: IntentSourceBrief, Path: "/x/brief.md"}, false},
		{"explicit", Intent{Source: IntentSourceExplicit, Path: "/x/intent.md"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			homeDir, jigHome := newIntentInferHomes(t)
			dir := newReviewLease(t, "main")
			writeReviewFile(t, dir, "alpha/alpha.go", "package alpha\n")
			writeReviewFile(t, dir, "alpha/percent.go", "package alpha\n")
			commitReviewLease(t, dir, "change")
			writeSyntheticClaudeSession(t, homeDir, "session-match", dir, []string{"alpha/alpha.go", "alpha/percent.go"}, intentInferSessionMTime)
			in := RoundInput{
				Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
				RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
				Home: jigHome, UserHome: homeDir, Intent: tc.intent, Recorded: true,
			}

			prep, err := PrepareReviewRequest(in)
			if err != nil {
				t.Fatalf("PrepareReviewRequest: %v", err)
			}
			if prep.Empty {
				t.Fatal("the round has a diff, but PrepareReviewRequest reports it empty")
			}
			got := prep.Request.IntentExcerpt
			if tc.want {
				if want := filepath.Join(home.IntentExcerptDir(jigHome), "JIG-1", "session-match.md"); got != want {
					t.Fatalf("intent_excerpt = %q, want %q", got, want)
				}
			} else if got != "" || prep.IntentNote != "" {
				t.Fatalf("intent_excerpt = %q (note %q), want none when the intent is stated", got, prep.IntentNote)
			}
			// The same request is on disk for the reviewer, under the jig home.
			onDisk := readReviewRequest(t, prep.RequestPath)
			if onDisk.IntentExcerpt != got || onDisk.Worktree != absPath(dir) || onDisk.HeadSHA != prep.Head {
				t.Fatalf("the reviewer's request = %+v, want excerpt %q, worktree %q and head %s", onDisk, got, absPath(dir), prep.Head)
			}
			if !strings.HasPrefix(prep.RequestPath, absPath(jigHome)) {
				t.Errorf("the reviewer's request %s is not under the jig home %s", prep.RequestPath, jigHome)
			}
			// The store's review.json is shared: it names none of this
			// machine's directories, and holds the rest of the request.
			inStore := readReviewRequest(t, prep.ReviewPath)
			if inStore.IntentExcerpt != "" || inStore.Worktree != "" || inStore.Recordings != "" || inStore.HeadSHA != prep.Head {
				t.Fatalf("the store's review.json = %+v, want no excerpt, worktree or recordings path and head %s", inStore, prep.Head)
			}
		})
	}
}

// TestPrepareReviewRequestSaysWhyThereIsNoExcerpt: a round with no stated
// intent and no session to point at carries the reason, not a silent gap.
func TestPrepareReviewRequestSaysWhyThereIsNoExcerpt(t *testing.T) {
	t.Parallel()
	dir := newReviewLease(t, "main")
	writeReviewFile(t, dir, "alpha/alpha.go", "package alpha\n")
	commitReviewLease(t, dir, "change")
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Intent: Intent{Source: IntentSourceNone},
	}

	prep, err := PrepareReviewRequest(in)
	if err != nil {
		t.Fatalf("PrepareReviewRequest: %v", err)
	}
	if prep.Request.IntentExcerpt != "" || prep.IntentNote != "no mapped clone recorded for this repo" {
		t.Fatalf("intent_excerpt = %q, note = %q, want none and the reason", prep.Request.IntentExcerpt, prep.IntentNote)
	}
}

// TestPrepareReviewRequestWritesNothingForARoundWithNothingToReview: the scope
// diff changes no file and nothing is outstanding, so a launched round has
// nothing to hand a reviewer and writes no request; a recorded round still
// writes one, since someone has to say the round is clean.
func TestPrepareReviewRequestWritesNothingForARoundWithNothingToReview(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{false, true} {
		dir := newReviewLease(t, "main")
		in := RoundInput{
			Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
			RepoName: "fixture-repo", Target: "main", Intent: Intent{Source: IntentSourceBrief, Path: "/x/brief.md"},
			Recorded: recorded,
		}

		prep, err := PrepareReviewRequest(in)
		if err != nil {
			t.Fatalf("PrepareReviewRequest(recorded=%v): %v", recorded, err)
		}
		if !prep.Empty {
			t.Errorf("recorded=%v: Empty = false, want a round with no diff to be empty", recorded)
		}
		_, statErr := os.Stat(reviewJSONPath(in.Store, "JIG-1", 1))
		if recorded && statErr != nil {
			t.Errorf("recorded round: review.json not written: %v", statErr)
		}
		if !recorded && statErr == nil {
			t.Errorf("launched round with nothing to review wrote a review.json")
		}
	}
}
