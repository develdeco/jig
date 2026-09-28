package store

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// TicketBlockedBy is one blocker in a ticket's ticket.yaml: the blocking
// ticket's id and its merge kind, always spelled out explicitly.
type TicketBlockedBy struct {
	Ticket string `yaml:"ticket"`
	Kind   string `yaml:"kind"`
}

// ticketDepsFile is the wire shape of <ticket>/ticket.yaml.
type ticketDepsFile struct {
	BlockedBy []TicketBlockedBy `yaml:"blocked_by,omitempty"`
}

// ticketDepsFilePath returns the path to <ticket>/ticket.yaml.
func (s *Store) ticketDepsFilePath(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "ticket.yaml")
}

// ReadTicketDeps reads <ticket>/ticket.yaml. An absent file reads as no
// blockers: a ticket without the file has none.
func (s *Store) ReadTicketDeps(ticket string) ([]TicketBlockedBy, error) {
	data, err := os.ReadFile(s.ticketDepsFilePath(ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f ticketDepsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.BlockedBy, nil
}

// WriteTicketDeps writes <ticket>/ticket.yaml with deps, replacing any
// existing content. jig graduate calls this once per newly minted ticket
// that has at least one blocker, before writing that ticket's id back into
// its chart entry.
func (s *Store) WriteTicketDeps(ticket string, deps []TicketBlockedBy) error {
	path := s.ticketDepsFilePath(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	out, err := yaml.Marshal(ticketDepsFile{BlockedBy: deps})
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}
