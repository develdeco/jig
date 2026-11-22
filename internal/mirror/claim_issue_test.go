package mirror

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// TestClaimIssueRemovesItsRecordWhenTheLinkFails covers claimIssue's write
// reporting what it touched when it fails: the record is written, the link to
// the owner then fails (a name that cannot be a link), and Claim undoes the
// record, so nothing of the failed claim stays in the store.
func TestClaimIssueRemovesItsRecordWhenTheLinkFails(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	const repo = "owner/name"
	it := item{kind: "ticket", id: "record.yaml", ref: &recordRef{}}
	rec := githubRecord{Repo: repo, Issue: 7, NodeID: "I_7"}

	_, err := claimIssue(context.Background(), st, nil, it, repo, github.Issue{Number: 7}, rec, "claim")
	if err == nil {
		t.Fatal("claimIssue: want the link's refusal, got nil")
	}
	if store.IsUndoIncomplete(err) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want the link's own refusal with a complete undo", err)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Fatalf("store is dirty after the failed claim:\n%s", status)
	}
	if _, err := os.Stat(st.TrackerPath(githubEntry(repo, 7))); !os.IsNotExist(err) {
		t.Fatalf("the record folder survived the failed claim (stat err %v)", err)
	}
	if got := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); got != head {
		t.Fatalf("HEAD moved from %s to %s", head, got)
	}
}
