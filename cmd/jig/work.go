package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// The verbs that work a slice from a session (claim, context, ask, answer,
// note) share how they find the store, the ticket, the slice and the session:
// the store as every verb does, the rest through locate.Locate, so the
// ticket is the positional one or the one the branch binds, the slice is
// --slice, JIG_SLICE or the one the session claimed, and the session is
// --session or JIG_SESSION.

// workFlags are the flags those verbs share.
type workFlags struct {
	store, project, slice, session *string
}

// addWorkFlags registers the shared flags on fs. withSlice and withSession
// say which of the two the verb takes; the usage strings are the ones every
// verb of the family gives them.
func addWorkFlags(fs *flag.FlagSet, withSlice, withSession bool) workFlags {
	f := workFlags{
		store:   fs.String("store", "", "explicit store path"),
		project: fs.String("project", "", "project name, resolved via the machine mapping"),
	}
	if withSlice {
		f.slice = fs.String("slice", "", sliceFlagUsage)
	}
	if withSession {
		f.session = fs.String("session", "", sessionFlagUsage)
	}
	return f
}

const (
	sliceFlagUsage   = "slice the work is on (default: JIG_SLICE, else the session's latest claim)"
	sessionFlagUsage = "harness session id (default: JIG_SESSION, else CLAUDE_CODE_SESSION_ID)"
)

// val reads a flag the verb may not have registered.
func val(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// work is where a session's work is: the store, the ticket and the rest of
// what Locate named.
type work struct {
	St      *store.Store
	Cfg     project.Config
	MP      project.MachineProject
	JigHome string
	Where   locate.Where
	Ticket  string
	Session string
}

// resolveWork finds the store, then the ticket (ticketArg, else the branch
// binds one), the slice and the session. A ticket that cannot be found is a
// VALIDATION_ERROR naming the way to give one. The slice is left to the
// caller: Where.Slice is "" when none was named or claimed.
func resolveWork(e env, stdout io.Writer, f workFlags, ticketArg string) (work, error) {
	st, cfg, mp, jigHome, err := resolveStoreForProject(e, val(f.project), val(f.store), stdout)
	if err != nil {
		return work{}, err
	}
	cwd, err := e.getwd()
	if err != nil {
		return work{}, err
	}
	// The store is already resolved; Locate is asked for the rest. With an
	// explicit store or project the answer must be about that store.
	storeFlag := ""
	if val(f.store) != "" || val(f.project) != "" {
		storeFlag = st.Root
	}
	where, err := locate.Locate(jigHome, cwd, locate.Opts{
		StoreFlag: storeFlag,
		Ticket:    ticketArg,
		Slice:     val(f.slice),
		Session:   val(f.session),
		Getenv:    e.getenv,
		NoMap:     true,
	})
	if err != nil {
		return work{}, err
	}
	if where.Ticket == "" {
		return work{}, &axi.Error{
			Msg:  "no ticket: this directory is on no ticket's branch and none was named",
			Code: "VALIDATION_ERROR",
			Help: []string{"Name the ticket, or run `jig next` to see which one to take"},
		}
	}
	if where.TicketHow == locate.HowArgAlias {
		fmt.Fprintf(stdout, "%s is now %s\n", ticketArg, where.Ticket)
	}
	if err := requireTicket(st, where.Ticket); err != nil {
		return work{}, err
	}
	return work{St: st, Cfg: cfg, MP: mp, JigHome: jigHome, Where: where, Ticket: where.Ticket, Session: where.Session}, nil
}

// slice returns the slice the work is on, which must be one of the ticket.
// When none was named or claimed the error lists the ticket slices.
func (w work) slice() (store.Slice, error) {
	slices, err := w.St.ReadSlices(w.Ticket)
	if err != nil {
		return store.Slice{}, err
	}
	ids := make([]string, len(slices))
	for i, sl := range slices {
		ids[i] = sl.ID
	}
	if w.Where.Slice == "" {
		return store.Slice{}, &axi.Error{
			Msg:  fmt.Sprintf("no slice: name the slice of %s this is about", w.Ticket),
			Code: "VALIDATION_ERROR",
			Help: []string{
				"Pass --slice <id> (or set JIG_SLICE); the slices are " + strings.Join(ids, ", "),
				fmt.Sprintf("`jig next %s` shows which are ready", w.Ticket),
			},
		}
	}
	if sl, ok := sliceByID(slices, w.Where.Slice); ok {
		return sl, nil
	}
	return store.Slice{}, &axi.Error{
		Msg:  fmt.Sprintf("ticket %s has no slice %s", w.Ticket, w.Where.Slice),
		Code: "SLICE_NOT_FOUND",
		Help: []string{"The slices are " + strings.Join(ids, ", ")},
	}
}

// looksLikeTicket reports whether tok is shaped like a ticket id (KEY-N) and
// nothing else, so a verb that takes an optional ticket before free text can
// tell the two apart.
func looksLikeTicket(tok string) bool {
	return tok != "" && store.BranchLeadID(tok) == tok
}

// splitTicket takes the leading positional as the ticket when it is shaped
// like one; the rest is free text, joined with spaces. So `jig ask JIG-1 which
// tone?` asks JIG-1, and `jig ask which tone?` leaves the ticket to the branch.
// The ticket comes first: a text that begins with a ticket id needs the ticket
// before it.
func splitTicket(positionals []string) (ticket, text string) {
	if len(positionals) > 0 && looksLikeTicket(positionals[0]) {
		return positionals[0], strings.Join(positionals[1:], " ")
	}
	return "", strings.Join(positionals, " ")
}

// parseWithText is parseWithPositionals for a verb whose positionals are free
// text: everything after a bare "--" is text however it begins, so a text
// that starts with a dash is still one.
func parseWithText(stdout io.Writer, fs *flag.FlagSet, args []string) (positionals []string, handled bool, err error) {
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	positionals, handled, err = parseWithPositionals(stdout, fs, args)
	if handled || err != nil {
		return nil, handled, err
	}
	return append(positionals, tail...), false, nil
}

// buildDir is the checkout the work is happening in: the work tree of the
// working directory when it belongs to the repo the slice builds in (the clone
// itself, or a linked work tree of it), else the clone. "" when the project has
// no clone of the repo on this machine.
func (w work) buildDir() string {
	clone := w.Where.RepoDir
	if clone == "" {
		return ""
	}
	if there, err := gitx.CommonDir(w.Where.Cwd); err == nil {
		if here, err := gitx.CommonDir(clone); err == nil && gitx.SameDir(there, here) {
			if top, err := gitx.TopLevel(w.Where.Cwd); err == nil {
				return top
			}
		}
	}
	return clone
}
