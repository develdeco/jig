// Package tui is jig's terminal screens: the project, a ticket (its four steps,
// the sub-steps of one, the one that matters now opened into a card that tells
// its moves in the session's own words, and its activity), the ticket's brief,
// build, review and publish, and a slice. They are built only on the event stream and on running
// verbs: a ticket's lines arrive as events.Envelopes from a Source, what the
// store holds of its slices and asks arrives as the output of `jig status`,
// and every action a key makes is a verb run through a Runner (answer, triage,
// gate, publish). No screen reads a store. It backs `jig tui`.
//
// The model is a bubbletea model and nothing in it needs a terminal: View is
// plain text (colored when its renderer has a color profile), so each screen
// is tested by feeding it a stream and comparing what it draws. Run puts it on
// a terminal, or, for a pipe, prints each screen whole as it changes.
package tui
