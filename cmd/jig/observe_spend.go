package main

import (
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/spend"
)

// init registers spend as a note source of `jig observe` (internal/observe,
// notes.go): at a stop and a session end it records what the session has
// spent, from its transcript, and whenever the ticket is near or over its
// budget it tells the session and the person. The source never enforces:
// spend is reported, and nothing is stopped.
func init() {
	observe.RegisterNoteSource("spend", spend.Notes)
}
