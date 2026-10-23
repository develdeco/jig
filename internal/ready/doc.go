// Package ready decides what can be built next: which slices of a ticket are
// ready (every slice they wait on is green) and in what order a ready set is
// scheduled, which repo each slice's workspace belongs to, and which tickets
// of a store are ready to start. It holds the rules `jig next` and `jig here`
// share, taken out of the frontier that dispatched a ticket's slices to
// sessions and is gone.
//
// The functions are pure: they take what the store holds (slices, their
// states, tickets' blockers and journals) and return an answer, so the
// callers decide how to read it and a test needs no store. The two that move
// a slice back to queued by a person's word, Requeue and RequeueSlice
// (requeue.go), are the exception: they write the slice's state and journal.
package ready
