// Package ready decides what can be built next: which slices of a ticket are
// ready (every slice they wait on is green) and in what order a ready set is
// scheduled, which repo each slice's workspace belongs to, and which tickets
// of a store are ready to start. It holds the rules `jig next` and `jig here`
// share, taken out of internal/frontier so they need no session backend.
//
// The functions are pure: they take what the store holds (slices, their
// states, tickets' blockers and journals) and return an answer, so the
// callers decide how to read it and a test needs no store.
package ready
