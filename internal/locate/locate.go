// Package locate answers "where am I": from a directory alone it names the
// jig project, its store, the repo and branch, and the ticket, slice and
// session the work belongs to, and says how each was found. `jig here`
// prints the answer, and every verb resolves its store through it, so a clone
// that only carries a jig.yaml marker works as well as a mapped one.
//
// Locate reads; the one thing it writes is the machine map (projects.yaml
// under the jig home), to record a clone it recognized (a marker, or an
// origin remote a mapped project lists) so the next call finds it by path. It
// never touches a repo or a store, and it never syncs, pushes or mirrors.
package locate

import (
	"fmt"
	"path/filepath"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// SessionEnv is the variable a jig command takes its harness session id from
// when no --session names one. A harness's SessionStart hook sets it for the
// session's later shell commands where the harness allows (internal/observe).
const SessionEnv = "JIG_SESSION"

// How a Where was resolved: the first of these that matched, in this order.
const (
	// HowFlag: the --store flag named the store.
	HowFlag = "flag"
	// HowMarker: a jig.yaml marker found walking up from the directory.
	HowMarker = "marker"
	// HowMapPath: the machine map (projects.yaml) has a clone containing
	// the directory, or the directory is a linked worktree of one.
	HowMapPath = "map-path"
	// HowMapRemote: the directory's git origin matches a repo remote of a
	// project in the machine map.
	HowMapRemote = "map-remote"
	// HowStore: the directory is inside a store itself, or next to the one
	// `jig init --standalone` made for it.
	HowStore = "store"
	// HowNone: none of the above; the directory is not in a jig project.
	HowNone = "none"
)

// How a ticket, slice or session was found (Where.TicketHow, SliceHow and
// SessionHow).
const (
	// HowArg: named explicitly, as a positional or a flag.
	HowArg = "arg"
	// HowArgAlias: named explicitly by an id the ticket carried before.
	HowArgAlias = "arg-alias"
	// HowBranch: the branch name leads with the ticket's id.
	HowBranch = "branch"
	// HowBranchAlias: the branch name leads with an id the ticket carried
	// before.
	HowBranchAlias = "branch-alias"
	// HowEnv: the JIG_SLICE or JIG_SESSION variable.
	HowEnv = "env"
	// HowClaim: the session's latest claim line in the ticket's journal.
	HowClaim = "claim"
)

// Where is the answer to "where am I".
type Where struct {
	// How is how the project was found: one of the How constants.
	How string
	// Explain is one line per step the resolution took, in order, for
	// `jig here --explain`.
	Explain []string

	// JigHome is the jig home root the answer was resolved against, and Cwd
	// the directory it was resolved from.
	JigHome string
	Cwd     string

	// Project is the project's name, and Kind what the directory is to it:
	// project.KindProject, KindOrchestrator or KindMember.
	Project string
	Kind    string
	// Root is the directory the marker (or the mapped clone) is rooted at.
	Root string
	// StorePath is the store's directory on this machine, "" when it is not
	// cloned here, and StoreRemote the store's remote, from the marker.
	StorePath   string
	StoreRemote string

	// Repo is the repo the directory is in, by its name in the project, and
	// RepoDir that repo's directory on this machine (the clone, even when
	// Cwd is in a linked worktree of it).
	Repo    string
	RepoDir string
	// Branch is the branch checked out where Cwd is ("" when detached).
	Branch string

	// Ticket is the ticket the work is on, and TicketHow where that came
	// from: a positional id, the branch name, and so on (HowArg ...).
	Ticket    string
	TicketHow string
	// Slice and SliceHow are the same for the slice.
	Slice    string
	SliceHow string
	// Session and SessionHow are the same for the harness session.
	Session    string
	SessionHow string

	// Nested lists the member repos' directories when Cwd is in an
	// orchestrator, and Mapped is true when this call added the project to
	// the machine map.
	Nested []string
	Mapped bool
}

// Opts are Locate's inputs besides where it looks.
type Opts struct {
	// StoreFlag is the --store flag's value, "" when not given.
	StoreFlag string
	// Ticket, Slice and Session are the positional or flag values that name
	// them explicitly, "" when not given.
	Ticket  string
	Slice   string
	Session string
	// Getenv reads the environment (JIG_SLICE, JIG_SESSION); it is never the
	// process's own, so a test gives each call an environment of its own. A
	// nil Getenv reads an empty one.
	Getenv func(string) string
	// NoMap leaves the machine map alone: Locate then never adds the project
	// it found to it.
	NoMap bool
	// StoreOnly resolves the project and its store and stops there: no repo
	// branch, ticket, slice or session. A verb that only needs its store
	// asks for this, and spends no git call on the rest.
	StoreOnly bool
}

// Locate resolves where cwd is under the jig home root jigHome. The order is
// fixed, and the first step that answers wins:
//
//  1. --store (Opts.StoreFlag): HowFlag.
//  2. A jig.yaml marker found walking up from cwd: HowMarker. The store is the
//     machine map's entry for the marker's project, else the sibling
//     <root>/../<project>-tickets holding a project.yaml (v2) or store.yaml
//     (v3), else only the marker's store remote (Where.StoreRemote, no
//     StorePath).
//  3. The machine map by path: the longest mapped clone containing cwd, or
//     the clone cwd is a linked worktree of: HowMapPath.
//  4. The machine map by remote: cwd's git origin against the repo remotes of
//     every mapped project's store: HowMapRemote.
//  5. cwd inside a store, or next to its standalone store: HowStore.
//  6. Otherwise HowNone, with no error: the caller stays silent.
//
// A directory under one `jig ignore` named is HowNone whatever else holds
// (--store aside). A clone found by marker or remote is added to the machine
// map on the spot, unless Opts.NoMap; a clone already mapped, or a repo whose
// mapped clone still exists, is left as it is.
//
// The ticket is Opts.Ticket, else the one the branch binds
// (Store.TicketForBranch); the slice is Opts.Slice, JIG_SLICE, else the
// session's latest claim line; the session is Opts.Session, else JIG_SESSION.
func Locate(jigHome, cwd string, o Opts) (Where, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Where{}, fmt.Errorf("locate: resolve %s: %w", cwd, err)
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return Where{}, err
	}
	l := &locator{jigHome: jigHome, o: o, cwd: abs, machine: machine}
	if l.o.Getenv == nil {
		l.o.Getenv = func(string) string { return "" }
	}
	l.w = Where{How: HowNone, JigHome: jigHome, Cwd: abs}
	if err := l.resolve(); err != nil {
		return Where{}, err
	}
	if l.w.How == HowNone {
		return l.w, nil
	}
	if !o.StoreOnly {
		if err := l.attach(); err != nil {
			return Where{}, err
		}
	}
	l.mapOnTheSpot()
	return l.w, nil
}

// locator is one Locate call's state.
type locator struct {
	jigHome string
	o       Opts
	cwd     string
	machine map[string]project.MachineProject
	w       Where

	// entry is what a successful lookup would add to the machine map.
	entry *mapEntry

	// The git facts about cwd, each asked of git at most once.
	topDone, rootDone bool
	top, root         string
}

func (l *locator) explain(format string, args ...any) {
	l.w.Explain = append(l.w.Explain, fmt.Sprintf(format, args...))
}

// attach fills in the repo's branch, the ticket, the slice and the session.
func (l *locator) attach() error {
	w := &l.w
	if w.RepoDir != "" {
		dir := w.RepoDir
		if l.cwdInRepo() {
			dir = l.cwd
		}
		if b := currentBranch(dir); b != "" {
			w.Branch = b
			l.explain("branch: %s checked out in %s", b, dir)
		} else {
			l.explain("branch: none checked out in %s (detached HEAD)", dir)
		}
	}

	if id := l.o.Session; id != "" {
		w.Session, w.SessionHow = id, HowArg
	} else if id := l.o.Getenv(SessionEnv); id != "" {
		w.Session, w.SessionHow = id, HowEnv
	}

	var st *store.Store
	if w.StorePath != "" && isStoreDir(w.StorePath) {
		if s, err := store.Open(w.StorePath); err == nil {
			st = s
		}
	}
	if st != nil {
		if err := l.attachTicket(st); err != nil {
			return err
		}
	} else if l.o.Ticket != "" {
		w.Ticket, w.TicketHow = l.o.Ticket, HowArg
	}

	switch {
	case l.o.Slice != "":
		w.Slice, w.SliceHow = l.o.Slice, HowArg
	case l.o.Getenv("JIG_SLICE") != "":
		w.Slice, w.SliceHow = l.o.Getenv("JIG_SLICE"), HowEnv
	case st != nil && w.Ticket != "" && w.Session != "":
		if slice := claimedSlice(st, w.Ticket, w.Session); slice != "" {
			w.Slice, w.SliceHow = slice, HowClaim
		}
	}
	if w.Slice != "" {
		l.explain("slice: %s (%s)", w.Slice, w.SliceHow)
	}
	if w.Session != "" {
		l.explain("session: %s (%s)", w.Session, w.SessionHow)
	}
	return nil
}

// attachTicket names the ticket: the explicit one, else the one the branch
// binds.
func (l *locator) attachTicket(st *store.Store) error {
	w := &l.w
	if l.o.Ticket != "" {
		id, err := st.ResolveTicket(l.o.Ticket)
		if err != nil {
			return err
		}
		w.Ticket, w.TicketHow = id, HowArg
		if id != l.o.Ticket {
			w.TicketHow = HowArgAlias
		}
		l.explain("ticket: %s (%s)", w.Ticket, w.TicketHow)
		return nil
	}
	if w.Branch == "" {
		return nil
	}
	id, how, err := st.TicketForBranch(w.Branch)
	switch {
	case err != nil:
		// A store that cannot say which ticket a branch leads with must not
		// stop every verb run on that branch: say so, bind nothing.
		l.explain("ticket: branch %s binds nothing: %v", w.Branch, err)
	case id == "":
		l.explain("ticket: branch %s leads with no ticket of this store", w.Branch)
	case how == "alias":
		w.Ticket, w.TicketHow = id, HowBranchAlias
		l.explain("ticket: %s (branch %s leads with an earlier id)", id, w.Branch)
	default:
		w.Ticket, w.TicketHow = id, HowBranch
		l.explain("ticket: %s (branch %s)", id, w.Branch)
	}
	return nil
}

// claimedSlice is the slice session's latest claim line on ticket names, ""
// when it has none or released it since.
func claimedSlice(st *store.Store, ticket, session string) string {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return ""
	}
	slice := ""
	for _, ln := range lines {
		if ln.Session != session {
			continue
		}
		switch ln.Event {
		case journal.EventClaim:
			slice = ln.Slice
		case journal.EventRelease:
			if ln.Slice == slice {
				slice = ""
			}
		}
	}
	return slice
}

// currentBranch is the branch checked out in dir, "" when there is none
// (detached HEAD, an unreadable repo).
func currentBranch(dir string) string {
	out, err := gitx.Run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// isStoreDir reports whether dir holds a store's config file: project.yaml of
// a v2 store, or store.yaml of a v3 one.
func isStoreDir(dir string) bool { return project.IsStoreDir(dir) }

// storeName is the project name a store declares (a v2 store's name:, a v3
// store's projects: when it serves one), "" when it cannot be read or does
// not say.
func storeName(storePath string) string {
	cfg, err := project.LoadStoreConfig(storePath)
	if err != nil {
		return ""
	}
	return cfg.Name
}
