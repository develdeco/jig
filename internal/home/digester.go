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

// AncestryPath returns the file this machine keeps whether one commit is in
// another's history in, <root>/cache/ancestry.txt, and OverlapsPath the one it
// keeps which files a branch changes and whether two branches merge in,
// <root>/cache/overlaps.txt. Such an answer is a fact for ever, since a commit id
// names its whole history, so the files are only ever added to, each is bounded
// by its size, and they are caches, never a store's record, safe to delete. They
// are two files so a read that needs the first reads none of the second.
func AncestryPath(root string) string {
	return filepath.Join(root, "cache", "ancestry.txt")
}

// OverlapsPath is the file of the overlap facts (see AncestryPath).
func OverlapsPath(root string) string {
	return filepath.Join(root, "cache", "overlaps.txt")
}
