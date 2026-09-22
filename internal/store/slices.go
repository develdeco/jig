package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// Slice is one entry of a ticket's slices.yaml.
type Slice struct {
	ID        string   `yaml:"id"`
	Workspace string   `yaml:"workspace"`
	Goal      string   `yaml:"goal"`
	Oracle    string   `yaml:"oracle"`
	Env       string   `yaml:"env,omitempty"`
	BlockedBy []string `yaml:"blocked_by"`
	FromBrief []string `yaml:"from_brief"`          // sha256 hex of brief section bodies
	FromGate  int      `yaml:"from_gate,omitempty"` // round number for fix slices
	Findings  []string `yaml:"findings,omitempty"`  // gate finding ids this slice resolves
}

// SliceFile is the wire shape of a ticket's slices.yaml.
type SliceFile struct {
	Slices []Slice `yaml:"slices"`
}

func (s *Store) slicesFile(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "slices.yaml")
}

// ReadSlices reads a ticket's slices.yaml. An absent file reads as no
// slices.
func (s *Store) ReadSlices(ticket string) ([]Slice, error) {
	data, err := os.ReadFile(s.slicesFile(ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sf SliceFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	return sf.Slices, nil
}

// AppendSlices appends add to a ticket's slices.yaml under a locked,
// read-modify-write cycle. It refuses when any id in add already exists in
// the file (or is repeated within add itself): a duplicate id would
// duplicate the slices.yaml row and reinitialize that slice's attempt
// counter, silently resurrecting a prior round's result.json.
func (s *Store) AppendSlices(ticket string, add []Slice) error {
	path := s.slicesFile(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	var sf SliceFile
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else if err := yaml.Unmarshal(data, &sf); err != nil {
		return err
	}

	existing := make(map[string]bool, len(sf.Slices))
	for _, s := range sf.Slices {
		existing[s.ID] = true
	}
	for _, s := range add {
		if existing[s.ID] {
			return &axi.Error{
				Msg:  fmt.Sprintf("slice id %q already exists in %s", s.ID, path),
				Code: "SLICE_ID_DUPLICATE",
			}
		}
		existing[s.ID] = true
	}

	sf.Slices = append(sf.Slices, add...)

	out, err := yaml.Marshal(sf)
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}
