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
// A phase screen (build, review, brief, publish) is a list and
// the detail of the row under the cursor: a Detail panel, drawn by one part
// (detail.go), holds that row's fields whole under the list at every width.
//
// Every panel is a pane with its own cursor and scroll: tab and shift+tab move
// the focus, the keys that move and act read the focused pane's row, the focused
// pane's border is lit and the hint line names it, a pane of text scrolls by
// lines, a pane that holds more rows than its room draws the window around its
// cursor and counts the rest, a row opens what it is about, and z draws the
// focused pane over the whole body (panes.go, zoom.go).
//
// Nothing a screen cuts is lost: a cut registers a cut mark, the cell its "…" is
// drawn on with the holder that owns the cut (a row with its lines and fields, a
// pane with no cursor rows, or the last command line) and the whole text it
// stands for. space, or a click on the mark, lays the holder out whole where it
// stands, the columns keeping the widths the folded rows gave them, and space
// folds it back; a row taller than its room is read by lines. An expansion is
// kept by the holder's identity and lasts as long as its screen
// (expand.go, fit.go, lastcmd.go).
//
// The slice screen tells the whole story of one slice, top to bottom: its
// finding when it is a fix slice, its goal, oracle and facts, every commit with
// its runs, the questions asked about it, the calls its holder made and its
// timeline. A run and a commit are worded once (records.go) for it and for the
// build screen's detail.
//
// The model is a bubbletea model and nothing in it needs a terminal: View is
// plain text (colored when its renderer has a color profile), so each screen
// is tested by feeding it a stream and comparing what it draws. Run puts it on
// a terminal, or, for a pipe, prints each screen whole as it changes.
package tui
