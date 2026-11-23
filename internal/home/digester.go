package home

import (
	"path/filepath"
)

// The digester's machine-local paths, all under the jig home root. None of
// them is ever in a store's git: the ledger and the logs are this machine's
// own, and a verify worktree is a scratch checkout.

// LedgerPath returns the machine ledger: <root>/ledger/ledger.ndjson, the
// NDJSON file every session, hook and detached worker on this machine
// appends to (internal/ledger).
func LedgerPath(root string) string {
	return filepath.Join(root, "ledger", "ledger.ndjson")
}

// HooksDir returns the directory jig keeps the hook scripts it installs in
// under the jig home root: <root>/hooks.
func HooksDir(root string) string {
	return filepath.Join(root, "hooks")
}

// LogsDir returns the directory the logs of jig's detached processes live
// in under the jig home root: <root>/logs.
func LogsDir(root string) string {
	return filepath.Join(root, "logs")
}

// PullRequestStatesPath returns the file this machine keeps what it has read
// of pull requests' states in: <root>/cache/pull-requests.json. It is a cache
// of the repo host's answers, never a store's record, and is safe to delete.
func PullRequestStatesPath(root string) string {
	return filepath.Join(root, "cache", "pull-requests.json")
}

// CommitFactsPath returns the file this machine keeps what it has asked git
// about commits in: whether one is in another's history, which files a branch
// changes, whether two branches merge, <root>/cache/commit-facts.txt. Such an
// answer is a fact for ever, since a commit id names its whole history, so the
// file is only ever added to; it is a cache, never a store's record, and is safe
// to delete.
func CommitFactsPath(root string) string {
	return filepath.Join(root, "cache", "commit-facts.txt")
}
