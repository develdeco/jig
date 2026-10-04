// Package tracker mints and projects ticket state onto an external tracker:
// jig's own local store, an operator-supplied command, GitHub Issues, or
// (in a future version) Jira/Linear. Callers depend only on the Adapter
// interface; New selects the concrete implementation from project config.
package tracker

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
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

// Graduation is a chart of tickets to mint together: the chart's name plus the
// ordered ticket drafts. A draft's BlockedBy may reference an earlier
// sibling by its 1-based position ("#k").
type Graduation struct {
	Chart   string
	Tickets []Draft
}

// indexRefRE matches a graduation BlockedBy entry that references the k-th
// minted sibling rather than an existing tracker id.
var indexRefRE = regexp.MustCompile(`^#(\d+)$`)

// CheckMinted refuses an id a tracker has just minted that jig cannot use (see
// pool.CheckTicket): a reserved suffix, or something that is not a single
// directory name. A tracker like github or a command tracker knows its id
// only once the ticket exists, so it cannot be asked beforehand: the refusal
// comes after the mint, and its help says the ticket now exists in the
// tracker and must be closed there. Every command that mints a ticket into
// the store (jig ticket new, jig graduate) runs this before it writes
// anything under the id, so an unusable id never gets a store folder or
// record, inside the store or, for a path-like id, outside it. It returns an
// *axi.Error, which a caller passes on as is.
func CheckMinted(a Adapter, id string) error {
	if err := pool.CheckTicket(id); err != nil {
		return &axi.Error{
			Msg:  fmt.Sprintf("the %s tracker minted %s, which jig cannot use: %v", a.Name(), id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("%s now exists in the %s tracker; close it there", id, a.Name()),
				"Change the tracker's id scheme so new ids are usable, then run this command again",
			},
		}
	}
	return nil
}

// Graduate mints every ticket in g.Tickets in order, resolving "#k"
// BlockedBy references to the id minted for the k-th ticket, and creates the
// store's ticket folder for each minted id. An id jig cannot use is refused
// (CheckMinted) before anything is written under it. Every minted ticket
// gets its <ticket>/ticket.yaml - the title and its resolved blockers, if
// any - written before the next ticket in g.Tickets is minted, the same
// record jig ticket new creates for a directly minted ticket; an id that
// already has a ticket.yaml is refused (store.ErrTicketRecordExists) rather
// than merged into. After each ticket is fully minted, onMinted (when
// non-nil) is called with its 0-based position in g.Tickets and its id, so a
// caller can record that ticket's id (e.g. write it back into a chart entry)
// before the next ticket is minted: a failure partway through then leaves
// every already-minted ticket recorded by the caller, and a re-run continues
// where it stopped instead of minting duplicates. Graduate returns the ids
// minted so far - complete on success, partial (with the error) on a failure
// partway through; an id is in the list as soon as the tracker minted it,
// whether or not it was then refused or recorded.
func Graduate(a Adapter, st *store.Store, g Graduation, onMinted func(i int, id string) error) ([]string, error) {
	ids := make([]string, len(g.Tickets))

	for i, d := range g.Tickets {
		resolved, err := resolveBlockedBy(d.BlockedBy, ids)
		if err != nil {
			return ids, fmt.Errorf("tracker: graduate: ticket %d: %w", i+1, err)
		}
		draft := d
		draft.BlockedBy = resolved
		id, err := a.Mint(draft)
		if err != nil {
			return ids, fmt.Errorf("tracker: graduate: mint ticket %d: %w", i+1, err)
		}
		ids[i] = id
		if err := CheckMinted(a, id); err != nil {
			return ids, err
		}
		if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
			return ids, fmt.Errorf("tracker: graduate: create store folder for %s: %w", id, err)
		}
		// Record the ticket the same way jig ticket new does for a directly
		// minted one, so consolidatedTitle (internal/verifydeliver) finds a
		// title for a graduated ticket too instead of falling all the way
		// back to the bare id: nothing else reads a graduated ticket's title
		// back from its tracker projection.
		rec := store.Ticket{Title: d.Title}
		for _, b := range resolved {
			rec.BlockedBy = append(rec.BlockedBy, store.TicketBlockedBy{Ticket: b.Ref, Kind: b.Kind})
		}
		if err := st.CreateTicketRecord(id, rec); err != nil {
			return ids, fmt.Errorf("tracker: graduate: write ticket.yaml for %s: %w", id, err)
		}
		if onMinted != nil {
			if err := onMinted(i, id); err != nil {
				return ids, err
			}
		}
	}

	return ids, nil
}

// resolveBlockedBy replaces any "#k" Ref in refs with ids[k-1] (the id
// minted for the k-th ticket so far), keeping each Blocker's Kind; anything
// else passes through unchanged, treated as an existing tracker id. It
// errors on a "#k" that names no minted ticket - out of range, or a sibling
// not minted yet - rather than writing the literal "#k" placeholder into the
// store as if it were a ticket id.
func resolveBlockedBy(refs []Blocker, ids []string) ([]Blocker, error) {
	if refs == nil {
		return nil, nil
	}
	out := make([]Blocker, len(refs))
	for i, r := range refs {
		if m := indexRefRE.FindStringSubmatch(r.Ref); m != nil {
			k, err := strconv.Atoi(m[1])
			if err != nil || k < 1 || k > len(ids) || ids[k-1] == "" {
				return nil, fmt.Errorf("blocked_by ref %q does not name an already-minted ticket", r.Ref)
			}
			out[i] = Blocker{Ref: ids[k-1], Kind: r.Kind}
			continue
		}
		out[i] = r
	}
	return out, nil
}
