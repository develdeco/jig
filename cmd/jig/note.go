package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// noteMax is the longest note: a note is a hand-off in a sentence or two, and
// it is carried in the journal, which every command reads whole.
const noteMax = 2000

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"note", "leave a note on a slice or ticket for whoever builds it next", []flagSpec{
			{"slice", sliceFlagUsage, false},
			{"session", sessionFlagUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 314,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdNote(e, args, stdout, stdin)
		},
	})
}

// cmdNote implements `jig note [<ticket>] [--slice s] [--session id]
// <text...>`: a note line in the journal of the ticket, on the slice when one
// is named or claimed, on the ticket as a whole otherwise. `jig context` shows
// the notes of a slice, and a hook hands them to the sessions working it.
func cmdNote(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "note")
	flags := addWorkFlags(fs, true, true)
	positionals, handled, err := parseWithText(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, text := splitTicket(positionals)
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return renderErr(stdout, &axi.Error{Msg: "missing the note: jig note <text...>", Code: "VALIDATION_ERROR"})
	case len(text) > noteMax:
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("a note is at most %d bytes, this one is %d", noteMax, len(text)),
			Code: "VALIDATION_ERROR",
			Help: []string{"Put the long material in the brief or a file, and note where it is"},
		})
	}

	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	slice := ""
	if w.Where.Slice != "" {
		sl, err := w.slice()
		if err != nil {
			return renderErr(stdout, err)
		}
		slice = sl.ID
	}
	if err := w.St.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	ref := noteFingerprint(w.Ticket, slice, text)
	err = journal.Append(w.St, w.Ticket, journal.Line{
		TS: stamp(e), Slice: slice, Event: journal.EventNote,
		Session: w.Session, Ref: ref, Provenance: defaultProvenance(e, stdin), Summary: text,
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	where := w.Ticket
	if slice != "" {
		where = fmt.Sprintf("%s slice %s", w.Ticket, slice)
	}
	if err := w.St.Push(fmt.Sprintf("%s: note", where)); err != nil {
		return renderErr(stdout, err)
	}
	pairs := [][2]string{{"ticket", w.Ticket}}
	if slice != "" {
		pairs = append(pairs, [2]string{"slice", slice})
	}
	pairs = append(pairs, [2]string{"note", ref})
	help := fmt.Sprintf("`jig context %s --slice <slice>` shows the notes on a slice", w.Ticket)
	if slice != "" {
		help = fmt.Sprintf("`jig context %s --slice %s` shows it with the rest", w.Ticket, slice)
	}
	axi.Render(stdout, axi.KV("noted", pairs), axi.Help(help))
	return 0
}

// noteFingerprint names a note by what it says and where: the first twelve
// hex digits of the digest of its ticket, slice and text.
func noteFingerprint(ticket, slice, text string) string {
	sum := sha256.Sum256([]byte(ticket + "\n" + slice + "\n" + text))
	return hex.EncodeToString(sum[:])[:12]
}

// defaultProvenance is who a line written at the command line speaks for: a
// person when stdin is a terminal, an agent otherwise.
func defaultProvenance(e env, stdin io.Reader) string {
	if e.stdinIsTerminal(stdin) {
		return "person"
	}
	return "agent"
}
