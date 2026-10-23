// Package verify runs one slice's oracle on one commit, in a detached git
// worktree made for it and in a detached process, and records the outcome in
// the ticket's journal: a pending line first, then the oracle line and, on a
// pass, the verified line, in the shapes the frontier wrote, so the gate can
// reuse a pass. It backs `jig verify`.
//
// Run is the whole verification, in the calling process: it is what the
// detached worker (`jig verify --worker`) and `jig verify --wait` call.
// Spawn is the foreground half: it checks the journal for a verification of
// the commit already, and starts the worker. Both leave the same trail: the
// verify-pending line, the oracle line, the recorded line when the scenarios
// recorded, the verified line and the slice's state on a pass, and for a run
// that could not reach the oracle a verify-error line.
package verify
