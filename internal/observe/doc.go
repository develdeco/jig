// Package observe turns what an agent harness reports (session start, a
// prompt, a tool call, a stop, session end) and git's post-commit into neutral
// Events, and acts on them: session records in the machine ledger, background
// verifications of new commits, and the context and notes handed back to the
// harness. A per-harness Adapter maps that harness's payload onto Event and
// the core's Result back onto what the harness reads; the core never reads a
// harness's own shape. It backs `jig observe` and the post-commit hook of
// `jig githook`.
//
// What happens at each event:
//
//   - session-start: a session-start line in the ledger, and the context of
//     `jig here` for the directory (the caller supplies it, as the sections of
//     that text live in cmd/jig).
//   - prompt: the notes the session has not heard.
//   - tool: a tool line in the ledger; when the call moved HEAD, the
//     verification of the new commit (internal/verify), once whoever saw it
//     first; the notes the session has not heard.
//   - stop: what is outstanding (verifications running, tracked files not
//     committed, questions open, notes) as a message to the person; for a
//     session under an unattended claim, a refusal to stop with that as the
//     reason, once for each finding.
//   - session-end: the session closed in the ledger, and a note line in the
//     ticket journal for a question left open.
//   - post-commit: a git-commit line in the ledger, and the verification of
//     the commit when it is on a ticket branch and names a slice.
//
// Nothing is kept between events but the ledger: what a session was told, and
// where HEAD was, are read back from the lines it wrote (memory.go). A
// directory in no jig project is left alone, with nothing written.
//
// A NoteSource (notes.go) is the extension point for what jig has to say to a
// session: corrections, spend. RegisterNoteSource adds one from an init().
package observe
