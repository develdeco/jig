package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// TicketBlockedBy is one blocker in a ticket's ticket.yaml: the blocking
// ticket's id and its merge kind, always spelled out explicitly.
type TicketBlockedBy struct {
	Ticket string `yaml:"ticket"`
	Kind   string `yaml:"kind"`
}

// Ticket is a ticket's own record, <ticket>/ticket.yaml: everything about
// the ticket that lives on the ticket itself rather than being read from
// its tracker or re-derived on every command. Every field is optional; a
// ticket with no ticket.yaml at all reads as a zero Ticket.
type Ticket struct {
	// Title is the ticket's headline, recorded right after minting by jig
	// ticket new and jig graduate, whatever the tracker.
	Title string
	// Branch is the ticket's adopted working branch. Empty means none is
	// recorded: callers resolve the branch through Store.TicketBranch,
	// never by reading this field directly, so the default ("jig/<ticket>")
	// stays in the one place that knows it.
	Branch string
	// BlockedBy is the ticket's own blockers, written by jig graduate.
	BlockedBy []TicketBlockedBy
}

// ticketFile is the wire shape of <ticket>/ticket.yaml. schema_version
// comes first for a human skimming the file; ticketSchemaVersion is the
// only value this jig version writes or expects.
type ticketFile struct {
	SchemaVersion int               `yaml:"schema_version"`
	Title         string            `yaml:"title,omitempty"`
	Branch        string            `yaml:"branch,omitempty"`
	BlockedBy     []TicketBlockedBy `yaml:"blocked_by,omitempty"`
}

// ticketSchemaVersion is the schema_version every ticket.yaml this jig
// version writes carries, and the newest one ReadTicket accepts.
const ticketSchemaVersion = 1

// ErrTicketRecordExists is what CreateTicketRecord wraps when the ticket
// already has a ticket.yaml, so a caller can tell that collision apart from
// any other write failure with errors.Is.
var ErrTicketRecordExists = errors.New("store: the ticket already has a ticket.yaml")

// TicketFilePath returns the path to <ticket>/ticket.yaml: the one place that
// names the file, for a caller that has to point the operator at it.
func (s *Store) TicketFilePath(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "ticket.yaml")
}

// ReadTicket reads <ticket>/ticket.yaml. An absent file reads as a zero
// Ticket: a ticket without the file has no title, no recorded branch, and
// no blockers.
//
// Decoding is strict: an unknown key (written by a newer jig version, or a
// typo from a hand edit) fails the read rather than being silently dropped
// the next time anything on this ticket is written. The failure is an
// *axi.Error, TICKET_RECORD_INVALID, whose help names both possible causes,
// since the read cannot tell them apart: upgrade jig, or fix the key. A
// caller that only wants to report the problem (jig graduate's drift
// advisory, jig validate) already treats a ReadTicketDeps/ReadTicket error
// that way; a caller that would otherwise write the file back
// (WriteTicketBranch) must not get the chance to lose the field it does not
// understand.
//
// A schema_version newer than ticketSchemaVersion is refused the same way,
// as an *axi.Error TICKET_SCHEMA_UNSUPPORTED, for a subtler reason a new
// field alone would not catch: a future jig could repurpose an existing
// key's meaning under a new schema_version without adding one KnownFields
// would notice. Every write goes through mutateTicket's own
// read-modify-write, so refusing here also keeps a write from ever
// re-marshaling such a file back out at today's schema_version, silently
// downgrading it.
//
// Any other failure (permission denied, a directory where the file should
// be) is the filesystem's own error, returned as is.
func (s *Store) ReadTicket(ticket string) (Ticket, error) {
	path := s.TicketFilePath(ticket)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Ticket{}, nil
		}
		return Ticket{}, err
	}
	var f ticketFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && err != io.EOF {
		return Ticket{}, &axi.Error{
			Msg:  fmt.Sprintf("%s could not be decoded: %v", path, err),
			Code: "TICKET_RECORD_INVALID",
			Help: []string{
				"If a newer jig version wrote a key this version does not know, upgrade jig",
				fmt.Sprintf("If the key is a typo or a hand edit, fix or remove it in %s", path),
			},
		}
	}
	if f.SchemaVersion > ticketSchemaVersion {
		return Ticket{}, &axi.Error{
			Msg:  fmt.Sprintf("%s has schema_version %d, newer than this jig version understands (%d)", path, f.SchemaVersion, ticketSchemaVersion),
			Code: "TICKET_SCHEMA_UNSUPPORTED",
			Help: []string{"Upgrade jig to a version that understands this ticket.yaml schema"},
		}
	}
	return Ticket{Title: f.Title, Branch: f.Branch, BlockedBy: f.BlockedBy}, nil
}

// ReadTicketDeps reads <ticket>/ticket.yaml's blockers. An absent file
// reads as no blockers: a ticket without the file has none.
func (s *Store) ReadTicketDeps(ticket string) ([]TicketBlockedBy, error) {
	t, err := s.ReadTicket(ticket)
	if err != nil {
		return nil, err
	}
	return t.BlockedBy, nil
}

// CreateTicketRecord writes ticket's first ticket.yaml, holding rec whole,
// under the store lock. It refuses, wrapping ErrTicketRecordExists, when the
// ticket already has one: no Mint (local, github, command) ever creates this
// file itself, so a freshly minted id that already has a record means some
// earlier, unrelated write claimed the id in the store, and merging into
// whatever that write recorded would silently mix two tickets. jig ticket
// new and jig graduate both record a minted ticket this way, so a minted
// ticket gets its record under one rule and in one write.
//
// The write's lock and AtomicWrite each create ticket.yaml's parent
// directory first, so this is also what gives a ticket whose tracker's Mint
// creates no store folder of its own (github, command) one.
func (s *Store) CreateTicketRecord(ticket string, rec Ticket) error {
	path := s.TicketFilePath(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrTicketRecordExists, path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeTicketFile(path, rec)
}

// mutateTicket reads <ticket>/ticket.yaml, applies fn to the decoded
// record, and writes the whole record back - all under one hold of the
// store lock. Every update of an existing ticket.yaml goes through this one
// read-modify-write, so a caller that changes a single field (WriteTicketBranch's
// branch) never has to know or restate the others, and never races another
// writer's own read-modify-write into dropping whichever field it changed.
func (s *Store) mutateTicket(ticket string, fn func(*Ticket)) error {
	path := s.TicketFilePath(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	cur, err := s.ReadTicket(ticket)
	if err != nil {
		return err
	}
	fn(&cur)
	return writeTicketFile(path, cur)
}

// writeTicketFile marshals t as a ticket.yaml at the current schema version
// and writes it atomically to path. Its callers hold the store lock.
func writeTicketFile(path string, t Ticket) error {
	out, err := yaml.Marshal(ticketFile{
		SchemaVersion: ticketSchemaVersion,
		Title:         t.Title,
		Branch:        t.Branch,
		BlockedBy:     t.BlockedBy,
	})
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}

// WriteTicketBranch sets <ticket>/ticket.yaml's branch to branch, leaving
// any recorded title and blockers untouched, and creating the record when
// the ticket has none. It is the one update of a record that already exists:
// a ticket's title and blockers are written once, whole, by
// CreateTicketRecord when the ticket is minted, and nothing changes them
// afterwards. Nothing calls this yet in v0.1: branch adoption is a later
// change; TicketBranch already resolves a recorded branch, so the write side
// can land separately from the read side that depends on it.
func (s *Store) WriteTicketBranch(ticket string, branch string) error {
	return s.mutateTicket(ticket, func(t *Ticket) { t.Branch = branch })
}

// TicketBranch returns ticket's working branch, resolved and validated
// against target: the branch recorded in its ticket.yaml, or "jig/<ticket>"
// when none is recorded. Every caller that names a ticket's branch resolves
// it here instead of hardcoding "jig/"+ticket, so the day something adopts
// a branch, every one of them picks it up - and every one of them gets this
// same validation for free, rather than each having to remember to run it
// after its own read. A caller that names the branch more than once in one
// command (publish, gate) resolves it once and passes the result down, so
// the whole command works on one branch even if ticket.yaml changes under it.
//
// A ticket.yaml ReadTicket refuses (an unknown key, a newer schema_version)
// is returned as is: its *axi.Error already carries its own code and the
// next step, which matters on this path, where every build lease, gate
// round and publish would otherwise fail with no more than a bare message.
//
// A recorded branch equal to target, one git itself would refuse as a ref
// name, or one git expands against this store's own checkout history
// ("@{-1}", the previously checked-out branch, which differs from one machine
// to the next), is never returned either: reconcile's merge and publish's
// guardedPush both trust whatever TicketBranch resolves, and target is the
// one branch where landing on it directly - skipping the PR - would matter.
// Both checks are of names, so a spelling git resolves on its own terms
// passes them: "refs/heads/main" for target, and "@", which git reads as HEAD
// wherever it parses a revision (a checkout of it stays where it is), for the
// name check. Nothing writes branch yet (WriteTicketBranch has no caller in
// v0.1), so today this only guards a hand-edited or otherwise externally
// written ticket.yaml, which `jig validate` checks the same way.
func (s *Store) TicketBranch(ticket, target string) (string, error) {
	rec, err := s.ReadTicket(ticket)
	if err != nil {
		return "", err
	}
	return s.ResolveTicketBranch(ticket, rec, target)
}

// ResolveTicketBranch is TicketBranch's resolution and validation of a record
// the caller has already read, for a command that needs something else from
// the same ticket.yaml too (publish's title): one read then serves both, and
// the record cannot change between them. rec must be ticket's own record, as
// ReadTicket returned it.
func (s *Store) ResolveTicketBranch(ticket string, rec Ticket, target string) (string, error) {
	if rec.Branch == "" {
		return "jig/" + ticket, nil
	}
	if rec.Branch == target {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which is also the target branch", ticket, rec.Branch),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{
				fmt.Sprintf("Record a branch other than %q in %s, or remove the branch key to use the default jig/%s", target, s.TicketFilePath(ticket), ticket),
			},
		}
	}
	// --branch prints the name it accepted, and expands "@{-N}" against this
	// repo's own reflog: whatever it printed must be the recorded name
	// itself, so a name that is only shorthand for another (which one
	// depends on the store's own checkout history, so on the machine) is
	// refused instead of resolved differently from one machine to the next.
	name, err := gitx.Run(s.Root, "check-ref-format", "--branch", rec.Branch)
	if err != nil {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which git rejects as a branch name: %v", ticket, rec.Branch, err),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{
				fmt.Sprintf("Fix the branch key in %s", s.TicketFilePath(ticket)),
			},
		}
	}
	if name != rec.Branch {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which git reads as shorthand for %q, not as a branch name", ticket, rec.Branch, name),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{
				fmt.Sprintf("Record the branch's own name in %s", s.TicketFilePath(ticket)),
			},
		}
	}
	return rec.Branch, nil
}
