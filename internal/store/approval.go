package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Approval is the record that a person approved a ticket's brief:
// <ticket>/approval.yaml. It names who approved, when, and the hash of the
// brief they approved, so a later edit of the brief shows as unapproved
// (BriefSHA256 no longer matches).
type Approval struct {
	// By and Email are the approver's name and email.
	By    string `yaml:"by"`
	Email string `yaml:"email,omitempty"`
	// At is when the approval was made, RFC3339.
	At string `yaml:"at"`
	// BriefSHA256 is the hex sha256 of brief.md as it was approved.
	BriefSHA256 string `yaml:"brief_sha256"`
	// Provenance says how the approval came about: "person" (the person
	// said so themselves), "agent" (an agent recorded the person's word) or
	// "jig".
	Provenance string `yaml:"provenance,omitempty"`
}

// approvalFile returns <ticket>/approval.yaml: the one place that names it.
func (s *Store) approvalFile(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "approval.yaml")
}

// ReadApproval reads ticket's approval record. ok is false, with no error,
// when the ticket has none.
func (s *Store) ReadApproval(ticket string) (a Approval, ok bool, err error) {
	data, err := os.ReadFile(s.approvalFile(ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return Approval{}, false, nil
		}
		return Approval{}, false, err
	}
	if err := yaml.Unmarshal(data, &a); err != nil {
		return Approval{}, false, err
	}
	return a, true, nil
}

// WriteApproval records a as ticket's approval, replacing any earlier one,
// under the file's lock, atomically.
func (s *Store) WriteApproval(ticket string, a Approval) error {
	path := s.approvalFile(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	return AtomicWrite(path, data)
}

// BriefSHA256 returns the hex sha256 of ticket's brief.md exactly as it is on
// disk: what an Approval records and what a later check compares it to. A
// ticket with no brief is an error, since there is nothing to approve.
func (s *Store) BriefSHA256(ticket string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.TicketDir(ticket), "brief.md"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
