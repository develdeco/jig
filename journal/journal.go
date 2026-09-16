// Package journal is the append-only NDJSON record of everything jig does
// to a ticket, and the pure renderers that turn it into human-readable
// changelogs.
package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/store"
)

// Line is one NDJSON record in a ticket's journal. Wire keys are exact.
type Line struct {
	TS      string `json:"ts"` // RFC3339; filled by Append if empty
	Ticket  string `json:"ticket"`
	Slice   string `json:"slice,omitempty"`
	Event   string `json:"event"`
	Outcome string `json:"outcome,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Model   string `json:"model,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

func journalPath(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "journal.ndjson")
}

// Append writes l as one NDJSON line to <ticket>/journal.ndjson, under a
// lock on the journal file. l.Ticket is set to ticket, and l.TS defaults to
// now (RFC3339) when empty.
func Append(st *store.Store, ticket string, l Line) error {
	if l.TS == "" {
		l.TS = time.Now().UTC().Format(time.RFC3339)
	}
	l.Ticket = ticket

	path := journalPath(st, ticket)
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	encoded, err := json.Marshal(l)
	if err != nil {
		return err
	}
	data = append(data, encoded...)
	data = append(data, '\n')

	return store.AtomicWrite(path, data)
}

// Read returns every line of a ticket's journal, in append order. Readers
// never lock. An absent journal reads as no lines.
func Read(st *store.Store, ticket string) ([]Line, error) {
	data, err := os.ReadFile(journalPath(st, ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var lines []Line
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var l Line
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// BuilderModels returns the distinct models used by event=dispatch lines,
// in first-seen order.
func BuilderModels(lines []Line) []string {
	seen := map[string]bool{}
	var models []string
	for _, l := range lines {
		if l.Event != "dispatch" || l.Model == "" || seen[l.Model] {
			continue
		}
		seen[l.Model] = true
		models = append(models, l.Model)
	}
	return models
}
