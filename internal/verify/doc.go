// Package verify runs one slice's oracle in the caller's own worktree, in the
// foreground and the caller's environment, and records the run in the
// ticket's journal: one oracle line that holds every input and fact needed to
// run it again by hand and, when the run covers HEAD, the recorded line, the
// slice's state and the verified line, in the shapes the frontier wrote, so
// the gate can reuse a pass. It backs `jig verify`. jig witnesses the run and
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
package verify
