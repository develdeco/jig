// Package locate answers "where am I": from a directory alone it names the
// jig project, its store, the repo and branch, and the ticket, slice and
// session the work belongs to, and says how each was found. `jig here` prints
// the answer, and every verb resolves its store through it, so a clone that
// only carries a jig.yaml marker works as well as a mapped one.
//
// This file holds the types and the entry point's signature; Locate itself is
// not written yet and returns ErrNotImplemented.
package locate

import "errors"

// ErrNotImplemented is what Locate returns until the resolver lands.
var ErrNotImplemented = errors.New("locate: not implemented")

// How a Where was resolved: the first of these that matched, in this order.
const (
	// HowFlag: the --store flag named the store.
	HowFlag = "flag"
	// HowMarker: a jig.yaml marker found walking up from the directory.
	HowMarker = "marker"
	// HowMapPath: the machine map (projects.yaml) has a clone containing
	// the directory.
	HowMapPath = "map-path"
	// HowMapRemote: the directory's git origin matches a repo remote of a
	// project in the machine map.
	HowMapRemote = "map-remote"
	// HowStore: the directory is inside a store itself.
	HowStore = "store"
	// HowNone: none of the above; the directory is not in a jig project.
	HowNone = "none"
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
	// RepoDir that repo's directory on this machine.
	Repo    string
	RepoDir string
	// Branch is the branch checked out in RepoDir.
	Branch string

	// Ticket is the ticket the work is on, and TicketHow where that came
	// from: a positional id, the branch name, a session's claim, and so on.
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
	// process's own, so a test gives each call an environment of its own.
	Getenv func(string) string
	// NoMap leaves the machine map alone: Locate then never adds the project
	// it found to it.
	NoMap bool
}

// Locate resolves where cwd is under the jig home root jigHome. It is not
// implemented yet: it returns ErrNotImplemented.
func Locate(jigHome, cwd string, o Opts) (Where, error) {
	return Where{}, ErrNotImplemented
}
