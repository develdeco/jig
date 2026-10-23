package main

import (
	"errors"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
)

// This file is `jig here`'s session section. A caller that works in a project
// without a session id (a person at a terminal, a harness whose hooks are not
// installed) gets one here: `jig here` finds the directory belongs to a
// project, mints a session id, records a session-start line for it, and
// prints the id, so the caller can pass it on as --session or JIG_SESSION. A
// caller that already has a session, from --session or JIG_SESSION, or a
// directory that is in no project, gets nothing from this section and the
// ledger is not written.

// sessionHowMinted is the "how" the section reports for an id it minted; the
// ones a caller brought say how they were given (locate.Where.SessionHow).
const sessionHowMinted = "minted"

// init registers this file's section with `jig here` (sections.go).
func init() {
	registerHereSection(hereSection{
		Order:  340,
		Name:   "session",
		Blocks: hereSessionBlocks,
		JSON:   hereSessionJSON,
	})
}

// hereSessionBlocks is the section's text: a `session:` block for a session it
// minted, nothing otherwise.
func hereSessionBlocks(c hereCtx) ([]string, error) {
	id, err := hereMintSession(c)
	if err != nil || id == "" {
		return nil, err
	}
	return []string{axi.KV("session", [][2]string{
		{"id", id},
		{"how", sessionHowMinted},
		{"env", "JIG_SESSION=" + id},
	})}, nil
}

// hereSessionJSON is the section's part of `--json`: the minted session's id
// and how it came to be, nothing otherwise.
func hereSessionJSON(c hereCtx) (any, error) {
	id, err := hereMintSession(c)
	if err != nil || id == "" {
		return nil, err
	}
	return struct {
		ID  string `json:"id"`
		How string `json:"how"`
	}{id, sessionHowMinted}, nil
}

// hereMintSession mints a session for the directory c.Where describes and
// records it, returning its id; it returns "" and writes nothing when the
// directory is in no project (no How, or HowNone) or a session was given.
// The session-start line carries what `jig here` found: the directory, its
// clone, the ticket and the slice.
func hereMintSession(c hereCtx) (string, error) {
	w := c.Where
	if w.How == "" || w.How == locate.HowNone || w.Session != "" {
		return "", nil
	}
	if c.JigHome == "" {
		return "", errors.New("jig here: no jig home to record a session in")
	}
	id := ledger.MintID()
	if err := ledger.StartSession(c.JigHome, c.e.now(), ledger.Registration{
		ID:     id,
		Cwd:    w.Cwd,
		Clone:  w.RepoDir,
		Ticket: w.Ticket,
		Slice:  w.Slice,
	}); err != nil {
		return "", err
	}
	return id, nil
}
