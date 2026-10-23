// Package events merges the ticket journals and the machine ledger into one
// stream of envelopes, ordered by time and tagged with where each line came
// from, and follows that stream as it grows. It backs `jig events` and is what
// the terminal screens read.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package events
