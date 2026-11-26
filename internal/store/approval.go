package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Approval is the record that a person approved a ticket's brief:
// <ticket>/approval.yaml. It names who approved, when, and the hash of the
// brief they approved, so a later edit of the brief, or of its illustration,
// shows as unapproved (BriefSHA256 no longer matches).
type Approval struct {
	// By and Email are the approver's name and email.
	By    string `yaml:"by"`
	Email string `yaml:"email,omitempty"`
	// At is when the approval was made, RFC3339.
	At string `yaml:"at"`
	// BriefSHA256 is the brief's hash as it was approved: what
	// Store.BriefSHA256 returned for the ticket then.
	BriefSHA256 string `yaml:"brief_sha256"`
	// Provenance says how the approval came about: "person" (the person
	// said so themselves), "agent" (an agent recorded the person's word) or
	// "jig".
	Provenance string `yaml:"provenance,omitempty"`
	// Sections are the "## " sections the approval covered, in the brief's
	// order, so a later reader can say which of them changed. An approval
	// made before sections were recorded has none, and says so by it.
	Sections []ApprovedSection `yaml:"sections,omitempty"`
	// IllustrationSHA256 is the hash of the illustration as it was approved,
	// empty when the brief had none.
	IllustrationSHA256 string `yaml:"illustration_sha256,omitempty"`
}

// ApprovedSection is one section an approval covered: its heading and the
// hash BriefSection carries, the one a slice's from_brief cites.
type ApprovedSection struct {
	Heading string `yaml:"heading"`
	Hash    string `yaml:"hash"`
}

// approvalFile returns <ticket>/approval.yaml: the one place that names it.
func (s *Store) approvalFile(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "approval.yaml")
}

// ReadApproval reads ticket's approval record. ok is false, with no error,
// when the ticket has none.
func (s *Store) ReadApproval(ticket string) (a Approval, ok bool, err error) {
	data, err := ReadFile(s.approvalFile(ticket))
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

// IllustrationFile is the name of a brief's illustration: an optional HTML
// file beside brief.md that the approval covers along with the brief's text.
const IllustrationFile = "illustration.html"

// BriefSHA256 returns the hash of ticket's brief as it is on disk: what an
// Approval records and what a later check compares it to.
//
// A brief with no illustration hashes to the hex sha256 of brief.md exactly as
// the file is, so an approval recorded before briefs had illustrations stays
// valid. A brief with one (IllustrationFile) hashes both files: the hex sha256
// of a two-line text that names each file with the hex sha256 of its bytes. No
// byte can move from one file to the other without changing that hash, and
// adding, editing or removing the illustration changes it as editing brief.md
// does. A ticket with no brief is an error, since there is nothing to approve.
func (s *Store) BriefSHA256(ticket string) (string, error) {
	dir := s.TicketDir(ticket)
	brief, err := ReadFile(filepath.Join(dir, "brief.md"))
	if err != nil {
		return "", err
	}
	briefSum := sha256Hex(brief)
	illustration, err := ReadFile(filepath.Join(dir, IllustrationFile))
	if errors.Is(err, fs.ErrNotExist) {
		return briefSum, nil
	}
	if err != nil {
		return "", err
	}
	return sha256Hex([]byte("brief.md " + briefSum + "\n" + IllustrationFile + " " + sha256Hex(illustration) + "\n")), nil
}

// BriefRecord returns what an approval of ticket's brief as it is on disk
// records beyond the whole-brief hash: each section's heading and hash, and
// the illustration's hash ("" when the brief has none). A ticket with no brief
// is an error, as for BriefSHA256.
func (s *Store) BriefRecord(ticket string) (sections []ApprovedSection, illustration string, err error) {
	dir := s.TicketDir(ticket)
	brief, err := ReadFile(filepath.Join(dir, "brief.md"))
	if err != nil {
		return nil, "", err
	}
	for _, sec := range BriefSections(brief) {
		sections = append(sections, ApprovedSection{Heading: sec.Heading, Hash: sec.Hash})
	}
	ill, err := ReadFile(filepath.Join(dir, IllustrationFile))
	if errors.Is(err, fs.ErrNotExist) {
		return sections, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return sections, sha256Hex(ill), nil
}

// sha256Hex returns the hex sha256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
