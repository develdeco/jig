// Package verify runs one slice's oracle in the caller's own worktree, in the
// foreground and the caller's environment, and records the run in the
// ticket's journal: one oracle line that holds every input and fact needed to
// run it again by hand and, when the run covers HEAD, the recorded line, the
// slice's state and the verified line, in the shapes the frontier wrote, which
// the gate lists for its reviewer as the slice's witnessed pass. It backs `jig
// verify`. jig witnesses the run and
// starts none: nothing in this package starts a process of jig's.
//
// Run is the whole verification, in the calling process: it runs the command
// through sh, streams its output, keeps a bounded tail of it, and leaves one
// trail: the oracle line, and for a run that covers HEAD (the tree clean before
// and after, HEAD unmoved, the slice's own oracle, a descendant of the start
// sha) the recorded line when the scenarios recorded, the verified line and the
// slice's state. Any other run is recorded with the reason it covers nothing
// and changes no state; a run that could not reach its command leaves a
// verify-error line. Plan says what a run would do and writes nothing.
//
// Every reader of a ticket's verification says one of two things of a commit
// and slice: the result a run recorded for it, or that it has none. Truth is
// what the readers share (the journal and, with one git log, the branch's
// commits): Derive tells each commit's state and LastRuns each slice's last
// witnessed run, covering or not, with whether it covers the slice's last
// commit. A verify-pending line and a verify-spawned ledger line are an old
// worker's history that nothing here reads as work in flight.
//
// RunFinal is `jig verify --final`: the ticket's last run, of the union of its
// slices' commands at HEAD (every slice that builds in this worktree's repo,
// each oracle resolved for its slice's workspace, identical text once, as one
// sh command line of parenthesised commands joined by &&). It adds no command,
// writes one final-oracle line that covers HEAD under a slice run's rules and
// changes no slice's state. PlanFinal says what it would do.
package verify
