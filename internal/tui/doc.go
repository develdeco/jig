// Package tui is jig's terminal screens: the project (its tickets and its
// charts), a ticket (its five steps, the sub-steps of one, the one that matters
// now opened into its stages, its fields and its latest move, and one activity
// log of every line and call tagged with its sub-step), the ticket's brief,
// build, review, publish and land, a slice, and a chart (its map, its tickets in the
// order a run would take them, the ticket under the cursor in detail, what the
// run would do and where it ends, and the tickets' activity). They are built only on the event stream and on running
// verbs: a ticket's lines arrive as events.Envelopes from a Source, what the
// store holds of its slices and asks arrives as the output of `jig status`, a
// chart's standing as the output of `jig next <chart>` (the store listing,
// `jig next`, names the charts), and every action a key makes is a verb run
// through a Runner (answer, triage, gate, publish). No screen reads a store. It
// backs `jig tui`.
//
// On every screen every list of what happened reads newest first: activity, rounds, a slice's
// tries and timeline, fix slices, holds, landings and the tail. The derivations
// keep time order and newest.go turns a list at the draw; a list too long for
// its pane ends with "↓ n older", and a line that comes in leaves the cursor on
// its row and a page read below its top on the lines it showed (hold.go).
//
// One allocator (layout.go) lays out every list: measured columns are as wide as
// their widest cell, free columns share the rest and wrap their words under their
// column, and a window keeps each box whole and marks what lies above and below.
//
// Every section of every screen is a panel, and every list in one names its
// columns in a dim row laid out by the same call as its rows, kept under the top
// edge when a window cuts the list (parts.go). The row under the cursor is lit
// across its panel, and a panel can be drawn with its border lit.
//
// The model is a bubbletea model and nothing in it needs a terminal: View is
// plain text (colored when its renderer has a color profile), so each screen
// is tested by feeding it a stream and comparing what it draws. Run puts it on
// a terminal, or, for a pipe, prints each screen whole as it changes.
package tui
