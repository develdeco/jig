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

// Graduate mints every ticket in g.Tickets in order, resolving "#k"
// BlockedBy references to the id minted for the k-th ticket, and creates the
// store's ticket folder for each minted id. A minted ticket with at least
// one resolved blocker gets its <ticket>/ticket.yaml written before the next
// ticket in g.Tickets is minted. After each ticket is fully minted, onMinted
// (when non-nil) is called with its 0-based position in g.Tickets and its
// id, so a caller can record that ticket's id (e.g. write it back into a
// chart entry) before the next ticket is minted: a failure partway through
// then leaves every already-minted ticket recorded by the caller, and a
// re-run continues where it stopped instead of minting duplicates. Graduate
// returns the ids minted so far - complete on success, partial (with the
// error) on a failure partway through.
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
		if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
			return ids, fmt.Errorf("tracker: graduate: create store folder for %s: %w", id, err)
		}
		if len(resolved) > 0 {
			deps := make([]store.TicketBlockedBy, len(resolved))
			for j, b := range resolved {
				deps[j] = store.TicketBlockedBy{Ticket: b.Ref, Kind: b.Kind}
			}
			if err := st.WriteTicketDeps(id, deps); err != nil {
				return ids, fmt.Errorf("tracker: graduate: write ticket.yaml for %s: %w", id, err)
			}
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
