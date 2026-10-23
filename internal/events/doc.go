// Package events merges the ticket journals and the machine ledger into one
// stream of envelopes, ordered by time and tagged with where each line came
// from, and follows that stream as it grows. It backs `jig events` and is what
// the terminal screens read.
//
// A Scope says what the stream covers: the journals of some tickets (a
// function, so a ticket created while a follower runs is picked up) and, when
// it names a jig home, the machine ledger. Read returns the stream as it is,
// sorted; Follow emits it, then whatever is added to it, until its context
// ends.
//
// A reader here assumes per-file append order and nothing more. Two machines'
// lines reach a journal through a git merge that may reorder them, and the
// ledger knows only this machine, so there is no causality across files; the
// stream is ordered by each line's own timestamp, and Follow emits a line when
// it first sees it, which for a line that arrives late is after later ones.
package events
