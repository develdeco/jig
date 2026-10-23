// Package verify runs one slice's oracle on one commit, in a detached git
// worktree made for it and in a detached process, and records the outcome in
// the ticket's journal: a pending line first, then the oracle line and, on a
// pass, the verified line, in the shapes the frontier wrote, so the gate can
// reuse a pass. It backs `jig verify`.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package verify
