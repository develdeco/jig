package home

import (
	"path/filepath"
	"testing"
)

// TestDigesterPathsDeriveFromTheRootGiven: the ledger, hooks, logs and pull
// request cache paths, like every home-anchored path, come from the root passed
// in.
func TestDigesterPathsDeriveFromTheRootGiven(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if got, want := LedgerPath(root), filepath.Join(root, "ledger", "ledger.ndjson"); got != want {
		t.Errorf("LedgerPath = %q, want %q", got, want)
	}
	if got, want := HooksDir(root), filepath.Join(root, "hooks"); got != want {
		t.Errorf("HooksDir = %q, want %q", got, want)
	}
	if got, want := LogsDir(root), filepath.Join(root, "logs"); got != want {
		t.Errorf("LogsDir = %q, want %q", got, want)
	}
	if got, want := PullRequestStatesPath(root), filepath.Join(root, "cache", "pull-requests.json"); got != want {
		t.Errorf("PullRequestStatesPath = %q, want %q", got, want)
	}
}
