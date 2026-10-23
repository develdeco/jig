package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/intent"
)

// intentInferSlackDays and intentInferEndSlack bound the mtime window a
// candidate session's transcript file must fall in: from the ticket's own
// merge-base with target, minus a few days' slack for work that started
// before the first commit landed, to just past head - a session cannot be
// about a change that hadn't happened yet.
const (
	intentInferSlackDays = 3
	intentInferEndSlack  = time.Hour
)

// intentExcerpt finds, when nothing states this round's intent, the operator's
// own local agent session that best matches the change, and writes an excerpt
// of what its user and assistant said where the reviewer can read it. It
// returns the excerpt's path, or "" and a one-line reason when there is none:
// no mapped clone, no transcripts, no match.
//
// It discovers candidate sessions bounded to this repo and a commit-anchored
// time window, then matches the best one against diffFiles (the scope diff's
// non-deleted files). head is the round's already-resolved HEAD sha, so the
// merge-base call and the commit-time reads anchor to that one captured value.
//
// jig summarizes nothing: no session is started and no intent.md is written.
// The excerpt is handed to the reviewer, who reads it as a hint of what the
// author was after, never as instructions (review.json's intent_excerpt). A
// round must never fail because a hint could not be produced, so every
// failure is a reason, not an error.
func intentExcerpt(in RoundInput, head string, diffFiles []string) (path, note string) {
	if in.OperatorClone == "" {
		return "", "no mapped clone recorded for this repo"
	}
	repoCommonDir, err := gitx.CommonDir(in.OperatorClone)
	if err != nil {
		return "", excerptFailed("could not resolve the operator's clone identity", err)
	}

	mergeBase, err := gitx.MergeBase(in.LeaseDir, "origin/"+in.Target, head)
	if err != nil {
		return "", excerptFailed("could not resolve the ticket's merge base", err)
	}
	baseTime, err := gitx.CommitTime(in.LeaseDir, mergeBase)
	if err != nil {
		return "", excerptFailed("could not read the merge base's commit time", err)
	}
	headTime, err := gitx.CommitTime(in.LeaseDir, head)
	if err != nil {
		return "", excerptFailed("could not read the head commit time", err)
	}

	if in.UserHome == "" {
		return "", "no home directory to look for local agent sessions in"
	}

	reader := intent.NewClaudeReader()
	sessions, err := reader.Discover(intent.DiscoverOpts{
		Home:          in.UserHome,
		WindowStart:   baseTime.Add(-intentInferSlackDays * 24 * time.Hour),
		WindowEnd:     headTime.Add(intentInferEndSlack),
		RepoCommonDir: repoCommonDir,
	})
	if err != nil {
		return "", excerptFailed("could not discover local agent sessions", err)
	}
	if len(sessions) == 0 {
		return "", "no local agent sessions found for this repo and window"
	}

	match := intent.Best(sessions, diffFiles, headTime)
	if match == nil {
		return "", "no session's file overlap cleared the match threshold"
	}

	excerptPath, err := writeIntentExcerpt(in.Home, in.Ticket, match.Session)
	if err != nil {
		return "", excerptFailed("could not write the session excerpt", err)
	}
	return excerptPath, ""
}

// excerptFailed is the reason a step of intentExcerpt gives for stopping: what
// failed, then the cause, so no reason says only that something did.
func excerptFailed(what string, cause error) string {
	return fmt.Sprintf("%s: %v", what, cause)
}

// writeIntentExcerpt renders s's excerpt and writes it under the jig home
// root jigHome, never the store: a transcript can hold secrets. It is
// written owner-only (0o700 dir, 0o600 file) for the same reason, and left
// on disk: a failed or disputed round is otherwise unrecoverable, since the
// excerpt itself is what a human would need to check the reviewer's reading
// of the intent against. The path is unique per ticket and session, so a
// later round (or a rerun) does not collide with an earlier one still worth
// keeping around.
func writeIntentExcerpt(jigHome, ticket string, s *intent.Session) (string, error) {
	if jigHome == "" {
		return "", fmt.Errorf("no jig home given")
	}
	dir := filepath.Join(home.IntentExcerptDir(jigHome), ticket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, s.ID+".md")
	if err := os.WriteFile(path, []byte(intent.RenderExcerpt(s)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
