// Package intent discovers the local agent session behind a change and
// scores it against a gate round's own scope diff, so jig's gate can infer
// a hint of intent when a ticket has neither a brief nor an explicit one.
//
// Matching (Best) is one generic, structural algorithm - file-path overlap
// between the diff and a session's own transcript, anchored to the diff
// itself - and it stays advisory: it only ever picks which session a model
// then summarizes (the caller's own dispatch, not this package), never
// judges the change or the session's own prose.
package intent
