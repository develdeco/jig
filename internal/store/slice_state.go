package store

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// SliceState is the on-disk progress record for one slice of one ticket.
type SliceState struct {
	State    string `yaml:"state"` // queued|building|green|needs-input|env-blocked|stalled
	Attempts int    `yaml:"attempts"`
	Session  string `yaml:"session,omitempty"`
	Question string `yaml:"question,omitempty"` // question id when needs-input
	Reason   string `yaml:"reason,omitempty"`   // e.g. "attempt-cap", "stall", "env-up-failed"

	// Signature is the stall signature (outcome.Signature's output) that
	// tripped a stalled state, set on both stalled paths - repeat-failure
	// stall and attempt-cap - and cleared on every route out of that state:
	// green, `jig requeue --from-brief-diff` and `--slice`, and answering
	// the slice's question. Additive field: absent on states an older jig
	// wrote, which read back as the zero value ("-" in `jig status`'s
	// stalled table's matching-key column).
	Signature string `yaml:"signature,omitempty"`

	// StallSummary is the human-readable result summary (outcome.Result's
	// own text, not the normalized Signature) that tripped the stall,
	// carried alongside Signature - same two set points, same clearing
	// routes - purely for display: `jig status`'s stalled table shows this,
	// never Signature, which exists only to match repeat failures and reads
	// as normalized, digit-stripped text unfit to show a human. Additive
	// field: absent on states an older jig wrote or on a stall this field
	// predates, which read back as the zero value ("-" in the table).
	StallSummary string `yaml:"stall_summary,omitempty"`
}

// stateFile returns the on-disk path for a slice's state record: the store
// schema's "<ticket>/slices/<id>.state" (see ARCHITECTURE.md) (YAML content,
// ".state" extension). This is
// the single place that constructs the path; every reader/writer of slice
// state goes through it.
func (s *Store) stateFile(ticket, slice string) string {
	return filepath.Join(s.TicketDir(ticket), "slices", slice+".state")
}

// ReadSliceState reads the slice state; an absent file reads as the
// zero-attempt queued state.
func (s *Store) ReadSliceState(ticket, slice string) (SliceState, error) {
	data, err := os.ReadFile(s.stateFile(ticket, slice))
	if err != nil {
		if os.IsNotExist(err) {
			return SliceState{State: "queued"}, nil
		}
		return SliceState{}, err
	}
	var st SliceState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return SliceState{}, err
	}
	return st, nil
}

// WriteSliceState writes the slice state under the sidecar lock, atomically.
func (s *Store) WriteSliceState(ticket, slice string, st SliceState) error {
	path := s.stateFile(ticket, slice)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}
	return AtomicWrite(path, data)
}
