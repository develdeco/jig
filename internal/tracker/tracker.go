// Package tracker projects ticket state onto an external tracker: jig's own
// local store, an operator-supplied command, GitHub Issues, or (in a future
// version) Jira/Linear. jig ticket new and jig graduate no longer mint
// through this package - every id is minted through the store package's own
// ticket_format counter (store.Mint, store.Graduate) - but Adapter still
// declares Mint for the trackers this package still builds; a later ticket
// removes this package along with the tracker: config key it serves.
// Callers depend only on the Adapter interface; New selects the concrete
// implementation from project config.
package tracker

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// Blocker is one blocking reference on a Draft: the target ticket id (or,
// during graduation, a "#k" placeholder for the k-th sibling in the same
// Graduation.Tickets slice, 1-based) and its merge kind ("merged" or
// "stacked").
type Blocker struct {
	Ref  string
	Kind string
}

// Draft is a ticket to be minted: a title, a body, and blocking references.
type Draft struct {
	Title     string
	Body      string
	BlockedBy []Blocker
}

// Subtask is one slice/child projected under a ticket.
type Subtask struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	State     string   `yaml:"state"`
	BlockedBy []string `yaml:"blocked_by"`
}

// Projection is the full desired state of a ticket's tracker record.
type Projection struct {
	Description string
	Subtasks    []Subtask
	Comments    []string
}

// Adapter is the tracker port. Every method call must be safe to retry;
// Project in particular is an idempotent full projection of a ticket's
// current state.
type Adapter interface {
	Name() string
	Mint(d Draft) (id string, err error)
	Project(ticketID string, p Projection) error
	Comment(ticketID string, body string) error
}

// PRCreator is an optional capability an Adapter may implement: opening a
// pull request for one repo's reconciled branch. It is distinct from
// Project's tracker-ticket projection, since not every tracker kind (or
// every repo host) has a PR concept jig can open directly. Callers type-
// assert an Adapter to PRCreator rather than requiring it on the interface,
// so local/command/jira/linear adapters remain valid Adapters without it.
type PRCreator interface {
	CreatePR(head, base, title, bodyFile string) (url string, err error)
}

// PRUpdater is an optional capability an Adapter may implement beside
// PRCreator: finding the open pull request for a branch and replacing its
// body, so that publishing a branch that already has one updates it instead
// of opening a second. Callers type-assert an Adapter to PRUpdater, as they do
// to PRCreator; an adapter with only PRCreator is asked to create every time.
type PRUpdater interface {
	// FindOpenPR returns the URL of the open pull request from head into
	// base, or "" when there is none. Only that: a closed or merged pull
	// request from head is history, and an open one into another base is
	// another delivery, so neither is "the" pull request and the caller opens
	// one. A lookup that fails is an error, never "none": the caller would
	// open a second pull request on it.
	FindOpenPR(head, base string) (url string, err error)
	// UpdatePR replaces the body of the pull request at url with the content
	// of bodyFile. Its title, base and everything else stay as they are.
	UpdatePR(url, bodyFile string) error
}

// PRCommenter is an optional capability an Adapter may implement: posting
// a comment on the pull request with the review notes. It is called after
// publish creates or updates the pull request.
type PRCommenter interface {
	// CommentPR posts a comment on the pull request at url with the content
	// of bodyFile.
	CommentPR(url, bodyFile string) error
}

// PRCreatorWithMedia is an optional capability an Adapter may implement:
// creating a pull request with media attachment support.
type PRCreatorWithMedia interface {
	// CreatePRWithMedia opens a pull request with optional media files attached.
	// mediaDir is the directory containing the media files, mediaFiles are the
	// relative file names to attach (e.g., ["demo-1.mp4", "demo-2.gif"]).
	// attached reports whether the files were actually attached: false
	// whenever mediaFiles is empty or the adapter has no way to attach them
	// (an installed gh too old for --attach, say), so the caller can tell
	// the operator why a rendered ./demo-<n>.<ext> reference went out
	// unattached rather than staying silent about it.
	CreatePRWithMedia(head, base, title, bodyFile, mediaDir string, mediaFiles []string) (url string, attached bool, err error)
}

// PRUpdaterWithMedia is an optional capability an Adapter may implement:
// updating a pull request with media attachment support.
type PRUpdaterWithMedia interface {
	// UpdatePRWithMedia updates the body of the pull request at url with optional
	// media files attached. mediaDir is the directory containing the media files,
	// mediaFiles are the relative file names to attach. attached reports
	// whether the files were actually attached, the same as
	// PRCreatorWithMedia.CreatePRWithMedia's own.
	UpdatePRWithMedia(url, bodyFile, mediaDir string, mediaFiles []string) (attached bool, err error)
}

// PRBodyReader is an optional capability an Adapter may implement: reading
// a pull request's current body back. Publish uses it right after writing
// media references into a pull request's body, to check whether the
// tracker actually rewrote them.
type PRBodyReader interface {
	// ReadPRBody returns the current body of the pull request at url.
	ReadPRBody(url string) (string, error)
}

// New returns the Adapter selected by cfg.Tracker.
func New(cfg project.Config, st *store.Store) (Adapter, error) {
	switch cfg.Tracker {
	case "local":
		return newLocalAdapter(cfg, st), nil
	case "command":
		return newCommandAdapter(cfg, st), nil
	case "github":
		return newGithubAdapter(cfg)
	case "jira":
		return notImplementedAdapter{name: "jira"}, nil
	case "linear":
		return notImplementedAdapter{name: "linear"}, nil
	default:
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("unknown tracker %q", cfg.Tracker),
			Code: "VALIDATION_ERROR",
		}
	}
}

// notImplementedAdapter backs the jira and linear tracker values: it
// compiles and satisfies Adapter, but every call fails with a code the
// caller can surface directly.
type notImplementedAdapter struct{ name string }

func (a notImplementedAdapter) Name() string { return a.name }

func (a notImplementedAdapter) err() error {
	return &axi.Error{
		Msg:  fmt.Sprintf("%s adapter ships in a future version; use github, local, or command", a.name),
		Code: "NOT_IMPLEMENTED",
	}
}

func (a notImplementedAdapter) Mint(Draft) (string, error)       { return "", a.err() }
func (a notImplementedAdapter) Project(string, Projection) error { return a.err() }
func (a notImplementedAdapter) Comment(string, string) error     { return a.err() }
