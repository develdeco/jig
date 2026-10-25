// Package correct decides when a session needs a correction note: a slice that
// fails its oracle the same way twice (a stall), a brief that has drifted from
// what was verified against it, a commit that reaches outside the
// workspaces the ticket's slices build in, and a commit that names no slice, so
// that jig verifies nothing for it. The observe hook delivers the
// notes; this package decides them, and records each one it finds as a
// correction line of the ticket journal.
//
// Check is the one entry point. It reads the ticket (its journal, its slices,
// its brief) and the commit a tool call just made, finds what is true now,
// writes a correction line the first time it finds each fact, and returns the
// notes the session has to hear. The four conditions are one file each
// (stall.go, brief.go, scope.go, slice.go) and know nothing of hooks.
package correct
