package home

import (
	"fmt"
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

// shaDirLen is how much of a commit sha names a verify's directory and log:
// twelve hex digits tell commits apart for as long as a worktree lives, and
// keep the path short on Windows.
const shaDirLen = 12

// VerifyWorktreeDir returns where `jig verify` checks a commit out to run a
// slice's oracle: <root>/verify/<storeID>/<ticket>/<sha12>-<slice>, sha12
// being the commit's first twelve hex digits. The checkout is a detached git
// worktree of the operator's clone, removed when the run ends. Each part
// must name a single plain directory, so no spelling climbs out of the tree.
func VerifyWorktreeDir(root, storeID, ticket, sha, slice string) (string, error) {
	leaf, err := verifyLeaf(storeID, ticket, sha, slice)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "verify", storeID, ticket, leaf), nil
}

// VerifyLogPath returns the log one detached `jig verify` worker writes:
// <root>/logs/verify/<storeID>/<ticket>/<sha12>-<slice>.log. The journal
// line carries a 600-byte summary of a failure; the rest of the output is
// here.
func VerifyLogPath(root, storeID, ticket, sha, slice string) (string, error) {
	leaf, err := verifyLeaf(storeID, ticket, sha, slice)
	if err != nil {
		return "", err
	}
	return filepath.Join(LogsDir(root), "verify", storeID, ticket, leaf+".log"), nil
}

// verifyLeaf is the <sha12>-<slice> name a verify's worktree and log share,
// once every part has been checked to be a plain directory name.
func verifyLeaf(storeID, ticket, sha, slice string) (string, error) {
	for _, part := range []evidencePart{{"store id", storeID}, {"ticket", ticket}, {"sha", sha}, {"slice", slice}} {
		if !singleDirName(part.name) {
			return "", fmt.Errorf("home: verify %s %q must name a single plain directory", part.what, part.name)
		}
	}
	short := sha
	if len(short) > shaDirLen {
		short = short[:shaDirLen]
	}
	return fmt.Sprintf("%s-%s", short, slice), nil
}
