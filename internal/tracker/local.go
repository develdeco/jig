package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// lockTimeout is the sidecar-lock timeout used around every store write this
// adapter makes, matching every other store writer (internal/store/slice_state.go,
// internal/store/question.go, internal/journal/journal.go,
// internal/verifydeliver/render.go).
const lockTimeout = 30 * time.Second

// localAdapter projects tickets directly onto the store: a folder per
// ticket under the store root, with tracker state under <id>/tracker/.
type localAdapter struct {
	cfg project.Config
	st  *store.Store
}

func newLocalAdapter(cfg project.Config, st *store.Store) *localAdapter {
	return &localAdapter{cfg: cfg, st: st}
}

func (a *localAdapter) Name() string { return "local" }

// ticketPattern builds the regexp that recognizes store folders minted by
// cfg.TicketFormat (e.g. "JIG-{n}" -> "^JIG-(\d+)$").
func ticketPattern(format string) (*regexp.Regexp, error) {
	parts := strings.SplitN(format, "{n}", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("tracker: ticket_format %q has no {n} placeholder", format)
	}
	pattern := "^" + regexp.QuoteMeta(parts[0]) + `(\d+)` + regexp.QuoteMeta(parts[1]) + "$"
	return regexp.Compile(pattern)
}

// Mint scans the store root for folders matching cfg.TicketFormat, takes
// the highest numbered one, and creates the next id's folder plus its
// tracker/ticket.md.
func (a *localAdapter) Mint(d Draft) (string, error) {
	re, err := ticketPattern(a.cfg.TicketFormat)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(a.st.Root)
	if err != nil {
		return "", fmt.Errorf("tracker: local mint: read store root: %w", err)
	}
	maxN := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	id := a.cfg.MintLocalID(maxN + 1)
	// Refuse an id jig cannot use before anything is written for it: a
	// ticket_format can mint one that ends in a lease suffix, or one that is
	// not a single directory at all.
	if err := pool.CheckTicket(id); err != nil {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("ticket_format %q mints %s, which jig cannot use: %v", a.cfg.TicketFormat, id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{"Change ticket_format in project.yaml, then mint again"},
		}
	}
	dir := filepath.Join(a.st.TicketDir(id), "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("tracker: local mint: create %s: %w", dir, err)
	}
	content := ticketMD(d.Title, d.Body)
	ticketPath := filepath.Join(dir, "ticket.md")
	release, _, err := store.Lock(ticketPath, lockTimeout)
	if err != nil {
		return "", fmt.Errorf("tracker: local mint: lock ticket.md: %w", err)
	}
	defer release()
	if err := store.AtomicWrite(ticketPath, []byte(content)); err != nil {
		return "", fmt.Errorf("tracker: local mint: write ticket.md: %w", err)
	}
	return id, nil
}

// subtasksFile is the wire shape of tracker/subtasks.yaml.
type subtasksFile struct {
	Subtasks []Subtask `yaml:"subtasks"`
}

// Project rewrites tracker/ticket.md and tracker/subtasks.yaml for
// ticketID, then appends any comments in p.Comments. It is safe to call
// repeatedly: ticket.md and subtasks.yaml are always overwritten in full.
func (a *localAdapter) Project(ticketID string, p Projection) error {
	dir := filepath.Join(a.st.TicketDir(ticketID), "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tracker: local project: create %s: %w", dir, err)
	}
	desc := p.Description
	if !strings.HasSuffix(desc, "\n") {
		desc += "\n"
	}
	ticketPath := filepath.Join(dir, "ticket.md")
	releaseTicket, _, err := store.Lock(ticketPath, lockTimeout)
	if err != nil {
		return fmt.Errorf("tracker: local project: lock ticket.md: %w", err)
	}
	if err := store.AtomicWrite(ticketPath, []byte(desc)); err != nil {
		releaseTicket()
		return fmt.Errorf("tracker: local project: write ticket.md: %w", err)
	}
	releaseTicket()

	subtasks := p.Subtasks
	if subtasks == nil {
		subtasks = []Subtask{}
	}
	out, err := yaml.Marshal(subtasksFile{Subtasks: subtasks})
	if err != nil {
		return fmt.Errorf("tracker: local project: marshal subtasks.yaml: %w", err)
	}
	subtasksPath := filepath.Join(dir, "subtasks.yaml")
	releaseSubtasks, _, err := store.Lock(subtasksPath, lockTimeout)
	if err != nil {
		return fmt.Errorf("tracker: local project: lock subtasks.yaml: %w", err)
	}
	defer releaseSubtasks()
	if err := store.AtomicWrite(subtasksPath, out); err != nil {
		return fmt.Errorf("tracker: local project: write subtasks.yaml: %w", err)
	}

	for _, c := range p.Comments {
		if err := a.Comment(ticketID, c); err != nil {
			return err
		}
	}
	return nil
}

// commentFileRE matches an existing numbered comment file.
var commentFileRE = regexp.MustCompile(`^(\d{3})\.md$`)

// Comment appends body as the next numbered file under tracker/comments/.
// The whole read-modify-write (scanning for the highest existing number,
// then writing the next one) runs under the sidecar lock, like every other
// store read-modify-write (e.g. store.AppendSlices), so two concurrent
// comments never race for the same number.
func (a *localAdapter) Comment(ticketID string, body string) error {
	dir := filepath.Join(a.st.TicketDir(ticketID), "tracker", "comments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tracker: local comment: create %s: %w", dir, err)
	}

	release, _, err := store.Lock(dir, lockTimeout)
	if err != nil {
		return fmt.Errorf("tracker: local comment: lock %s: %w", dir, err)
	}
	defer release()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("tracker: local comment: read %s: %w", dir, err)
	}
	max := 0
	for _, e := range entries {
		m := commentFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err == nil && n > max {
			max = n
		}
	}
	name := fmt.Sprintf("%03d.md", max+1)
	content := body
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return store.AtomicWrite(filepath.Join(dir, name), []byte(content))
}

// ticketMD renders a minted ticket's initial description: an optional
// "# <title>" heading followed by the body.
func ticketMD(title, body string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("# " + title + "\n\n")
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
