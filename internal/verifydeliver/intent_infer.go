package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/intent"
	"github.com/develdeco/jig/internal/ledger"
)

// intentInferSlackDays and intentInferEndSlack bound the mtime window a
// candidate session's transcript file must fall in when the sessions are
// searched for: from the ticket's own merge-base with target, minus a few
// days' slack for work that started before the first commit landed, to just
// past head - a session cannot be about a change that hadn't happened yet.
const (
	intentInferSlackDays = 3
	intentInferEndSlack  = time.Hour
)

// intentExcerpt finds, when nothing states this round's intent, the operator's
// own local agent session behind the change, and writes an excerpt of what its
// user and assistant said where the reviewer can read it. It returns the
// excerpt's path, or "" and a one-line reason when there is none.
//
// The machine ledger knows which sessions worked the ticket, so it is asked
// first (ticketSessionExcerpt): the sessions it names are read from the
// transcript paths their hooks recorded, and nothing else is read. Only when
// that finds none, because no hook ran for the ticket, none reported a
// transcript or none can be read now, does it fall back to searching the
// operator's local transcripts for the one that best matches the change
// (searchedExcerpt), so a machine whose hooks never ran loses nothing.
//
// jig summarizes nothing: no session is started and no intent.md is written.
// The excerpt is handed to the reviewer, who reads it as a hint of what the
// author was after, never as instructions (review.json's intent_excerpt). A
// round must never fail because a hint could not be produced, so every
// failure is a reason, not an error.
func intentExcerpt(in RoundInput, head string, diffFiles []string) (path, note string) {
	if path, ok := ticketSessionExcerpt(in, diffFiles); ok {
		return path, ""
	}
	return searchedExcerpt(in, head, diffFiles)
}

// ticketSessionExcerpt writes the excerpt of the session the machine ledger
// holds for the ticket and reports whether it did. The candidates are the
// sessions that worked the ticket (ticketSessionIDs); each is read from the
// transcript path its start or touch line recorded, by the reader of its
// harness, and the one that mentions the most of diffFiles is excerpted
// (intent.Among). A candidate with no recorded transcript, no reader for its
// harness, or a transcript that is gone or not readable is passed over, and
// none left means false: the caller searches instead. Nothing here is an
// error, since the hint is never worth failing a round for.
func ticketSessionExcerpt(in RoundInput, diffFiles []string) (string, bool) {
	if in.Home == "" || in.Store == nil {
		return "", false
	}
	recorded, err := ledger.Sessions(in.Home)
	if err != nil {
		return "", false
	}
	var loaded []*intent.Session
	for _, id := range ticketSessionIDs(in) {
		rec, ok := recorded[id]
		if !ok || rec.Transcript == "" || !filepath.IsAbs(rec.Transcript) {
			continue
		}
		reader := intent.ReaderFor(rec.Harness)
		if reader == nil {
			continue
		}
		if s, err := reader.Load(rec.Transcript); err == nil && s != nil {
			loaded = append(loaded, s)
		}
	}
	match := intent.Among(loaded, diffFiles)
	if match == nil {
		return "", false
	}
	path, err := writeIntentExcerpt(in.Home, in.Ticket, match.Session)
	return path, err == nil
}

// ticketSessionIDs are the ids of the sessions that worked the ticket, the
// ticket's driver first: the driver the claims log names (a session of another
// machine is simply not in this machine's ledger), then the builders - the
// sessions on the journal's claim, verify-pending and oracle lines and on the
// ledger's commit and file-changing tool lines for the ticket or its branch
// (Builders). A tool line that changed no file is not counted, as it is not for
// independence: a reviewer reads the code with the same tools, and its session
// is no source of the author's intent.
func ticketSessionIDs(in RoundInput) []string {
	var ids []string
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if driver, ok, err := in.Store.Driver(in.Ticket); err == nil && ok {
		add(driver.Session)
	}
	builders, _ := buildersOf(in.Store, in.Home, in.Ticket, in.Branch)
	for _, b := range builders {
		add(b.Session)
	}
	return ids
}

// searchedExcerpt is the search intentExcerpt falls back to: it finds the
// operator's own local agent session that best matches the change, among all
// the transcripts of this repo in a commit-anchored time window, and writes
// its excerpt. It returns the excerpt's path, or "" and a one-line reason when
// there is none: no mapped clone, no transcripts, no match.
//
// It discovers candidate sessions bounded to this repo and a commit-anchored
// time window, then matches the best one against diffFiles (the scope diff's
// non-deleted files). head is the round's already-resolved HEAD sha, so the
// merge-base call and the commit-time reads anchor to that one captured value.
func searchedExcerpt(in RoundInput, head string, diffFiles []string) (path, note string) {
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
