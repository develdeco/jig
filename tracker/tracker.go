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

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/store"
)

// Draft is a ticket to be minted: a title, a body, and blocking references.
// BlockedBy entries are either existing tracker ids or, during graduation,
// "#k" indices into the same Graduation.Tickets slice (1-based).
type Draft struct {
	Title     string
	Body      string
	BlockedBy []string
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

// Graduation is a chart of tickets to mint together: an epic label plus the
// ordered ticket drafts. A draft's BlockedBy may reference an earlier
// sibling by its 1-based position ("#k").
type Graduation struct {
	Epic    string
	Tickets []Draft
}

// indexRefRE matches a graduation BlockedBy entry that references the k-th
// minted sibling rather than an existing tracker id.
var indexRefRE = regexp.MustCompile(`^#(\d+)$`)

// Graduate mints every ticket in g.Tickets in order, resolving "#k"
// BlockedBy references to the id minted for the k-th ticket, creates the
// store's ticket folder for each minted id, and projects the resulting
// blocking links onto each ticket. It returns the minted ids in order.
func Graduate(a Adapter, st *store.Store, g Graduation) ([]string, error) {
	ids := make([]string, len(g.Tickets))
	resolved := make([][]string, len(g.Tickets))

	for i, d := range g.Tickets {
		resolved[i] = resolveBlockedBy(d.BlockedBy, ids)
		draft := d
		draft.BlockedBy = resolved[i]
		id, err := a.Mint(draft)
		if err != nil {
			return nil, fmt.Errorf("tracker: graduate: mint ticket %d: %w", i+1, err)
		}
		ids[i] = id
		if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
			return nil, fmt.Errorf("tracker: graduate: create store folder for %s: %w", id, err)
		}
	}

	for i, d := range g.Tickets {
		p := Projection{
			Description: d.Body,
			Subtasks: []Subtask{{
				ID:        ids[i],
				Title:     d.Title,
				State:     "queued",
				BlockedBy: resolved[i],
			}},
		}
		if err := a.Project(ids[i], p); err != nil {
			return nil, fmt.Errorf("tracker: graduate: project ticket %s: %w", ids[i], err)
		}
	}
	return ids, nil
}

// resolveBlockedBy replaces any "#k" reference in refs with ids[k-1] (the
// id minted for the k-th ticket so far); anything else passes through
// unchanged, treated as an existing tracker id.
func resolveBlockedBy(refs []string, ids []string) []string {
	if refs == nil {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		if m := indexRefRE.FindStringSubmatch(r); m != nil {
			k, err := strconv.Atoi(m[1])
			if err == nil && k >= 1 && k <= len(ids) && ids[k-1] != "" {
				out[i] = ids[k-1]
				continue
			}
		}
		out[i] = r
	}
	return out
}
