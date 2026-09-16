package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/store"
)

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
	dir := filepath.Join(a.st.TicketDir(id), "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("tracker: local mint: create %s: %w", dir, err)
	}
	content := ticketMD(d.Title, d.Body)
	if err := store.AtomicWrite(filepath.Join(dir, "ticket.md"), []byte(content)); err != nil {
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
	if err := store.AtomicWrite(filepath.Join(dir, "ticket.md"), []byte(desc)); err != nil {
		return fmt.Errorf("tracker: local project: write ticket.md: %w", err)
	}

	subtasks := p.Subtasks
	if subtasks == nil {
		subtasks = []Subtask{}
	}
	out, err := yaml.Marshal(subtasksFile{Subtasks: subtasks})
	if err != nil {
		return fmt.Errorf("tracker: local project: marshal subtasks.yaml: %w", err)
	}
	if err := store.AtomicWrite(filepath.Join(dir, "subtasks.yaml"), out); err != nil {
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
func (a *localAdapter) Comment(ticketID string, body string) error {
	dir := filepath.Join(a.st.TicketDir(ticketID), "tracker", "comments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tracker: local comment: create %s: %w", dir, err)
	}
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
