// Package correct decides when a session needs a correction note: a slice that
// fails the same way twice (a stall), a brief that has drifted from what was
// verified, and edits outside the scope of the slice. The observe hook
// delivers the notes; this package only decides them.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package correct
