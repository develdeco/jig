package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// Body is the ticket's description, recorded right after minting by jig
	// ticket new --body and jig graduate (from the chart entry's own body).
	// Empty means no body was given; nothing distinguishes that from an
	// older ticket minted before this field existed.
	Body string
	// Slug is the short kebab-case tail of the ticket's default branch name,
	// <id>-<slug>; the function Slug derives it from the title. jig ticket
	// new and jig graduate record it when they mint the ticket, so a later
	// change of the title never renames the branch. A ticket minted before
	// the field existed has none: its slug is derived from its title when
	// the branch is named, and recorded when the branch is
	// (WriteTicketBranch). Empty is also what a title with no ASCII word
	// gives.
	Slug string
	// Branch is the ticket's adopted working branch. Empty means none is
	// recorded: callers resolve the branch through Store.TicketBranch,
	// never by reading this field directly, so the default ("<id>-<slug>")
	// stays in the one place that knows it. The default is never written
	// here: a recorded branch is an adopted one (Adopted).
	Branch string
	// Rank is the ticket's place in the owner's priority order, written by
	// jig rank: lower comes first. Nil means unranked, which `jig next`
	// orders after every ranked ticket. Two tickets may hold the same rank;
	// `jig next` orders them by id and says the rank is shared. Any integer
	// is a rank to read; jig rank writes whole numbers from 1.
	Rank *int
	// BlockedBy is the ticket's own blockers, written by jig graduate.
	BlockedBy []TicketBlockedBy
	// Aliases is every earlier id this ticket has carried. Every rewrite of
	// the record keeps them; nothing but L3's migration (out of scope here)
	// writes them for real - in this ticket only tests set them. Store.
	// ResolveTicket is the one place an alias turns back into this ticket's
	// current id.
	Aliases []string
	// Closed is the ticket's closure: set when it was closed without a pull
	// request (`jig close`), nil while it is open. See Closed.
	Closed *Closed
}

// Adopted reports whether the ticket adopted a branch: a recorded branch is
// an adopted one, since nothing but adoption (`jig gate --branch`) writes it.
// It is the one predicate every command that treats an adopted ticket
// differently asks - gate, run, solve, publish and status alike.
func (t Ticket) Adopted() bool { return t.Branch != "" }

// ticketFile is the wire shape of <ticket>/ticket.yaml. schema_version
// comes first for a human skimming the file; ticketSchemaVersion is the
// only value this jig version writes or expects.
type ticketFile struct {
	SchemaVersion int               `yaml:"schema_version"`
	Title         string            `yaml:"title,omitempty"`
	Body          literalString     `yaml:"body,omitempty"`
	Slug          string            `yaml:"slug,omitempty"`
	Branch        string            `yaml:"branch,omitempty"`
	Rank          *int              `yaml:"rank,omitempty"`
	BlockedBy     []TicketBlockedBy `yaml:"blocked_by,omitempty"`
	Aliases       []string          `yaml:"aliases,omitempty"`
	Closed        *closedFile       `yaml:"closed,omitempty"`
}

// literalString marshals as a YAML literal block scalar ("|"), the same
// style ChartEntry.Body is written in (internal/store/charts.go), even for
// a single-line value: ticket.yaml's body is the chart entry's body or the
// --body flag's value carried over verbatim, so it reads the same way in
// both places.
type literalString string

func (s literalString) MarshalYAML() (interface{}, error) {
	if s == "" {
		return "", nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(s), Style: yaml.LiteralStyle}, nil
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
	return ReadTicketFile(s.TicketFilePath(ticket))
}

// ReadTicketFile is ReadTicket against an explicit ticket.yaml path, rather
// than one TicketDir resolves: `jig store migrate` reads a v1 store's
// ticket.yaml directly at its old (pre-tickets/) location, before the
// folder move Store.TicketDir alone could ever name.
func ReadTicketFile(path string) (Ticket, error) {
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
	t := Ticket{Title: f.Title, Body: string(f.Body), Slug: f.Slug, Branch: f.Branch, Rank: f.Rank, BlockedBy: f.BlockedBy, Aliases: f.Aliases}
	if f.Closed != nil {
		c := Closed(*f.Closed)
		t.Closed = &c
	}
	return t, nil
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
	if err := writeTicketFile(path, rec); err != nil {
		return err
	}
	s.invalidateAliasClaims()
	return nil
}

// mutateTicket reads <ticket>/ticket.yaml, applies fn to the decoded
// record, and writes the whole record back - all under one hold of the
// store lock. Every update of an existing ticket.yaml goes through this one
// read-modify-write, so a caller that changes a single field (WriteTicketBranch's
// branch) never has to know or restate the others, and never races another
// writer's own read-modify-write into dropping whichever field it changed.
func (s *Store) mutateTicket(ticket string, fn func(*Ticket)) error {
	return s.updateTicket(ticket, func(t *Ticket) error {
		fn(t)
		return nil
	})
}

// updateTicket is mutateTicket for a change that can be refused: when fn
// returns an error nothing is written and the error is returned as it is.
func (s *Store) updateTicket(ticket string, fn func(*Ticket) error) error {
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
	if err := fn(&cur); err != nil {
		return err
	}
	if err := writeTicketFile(path, cur); err != nil {
		return err
	}
	s.invalidateAliasClaims()
	return nil
}

// writeTicketFile marshals t as a ticket.yaml at the current schema version
// and writes it atomically to path. Its callers hold the store lock.
func writeTicketFile(path string, t Ticket) error {
	f := ticketFile{
		SchemaVersion: ticketSchemaVersion,
		Title:         t.Title,
		Body:          literalString(t.Body),
		Slug:          t.Slug,
		Branch:        t.Branch,
		Rank:          t.Rank,
		BlockedBy:     t.BlockedBy,
		Aliases:       t.Aliases,
	}
	if t.Closed != nil {
		c := closedFile(*t.Closed)
		f.Closed = &c
	}
	out, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}

// WriteTicketBranch sets <ticket>/ticket.yaml's branch to branch, leaving
// any recorded title and blockers untouched, and creating the record when
// the ticket has none. With SetTicketRank it is an update of a record that
// already exists: a ticket's title and blockers are written once, whole, by
// CreateTicketRecord when the ticket is minted, and nothing changes them
// afterwards. A ticket records a branch when it adopts one (`jig gate
// --branch`) or when a session claims a slice on a branch no ticket's id
// leads, so a recorded branch is an adopted one; CheckAdoptableBranch is the
// check a name passes before it is written, since this does none.
//
// A ticket that has no slug yet (one minted before the field existed) gets
// the one its title gives, now: the slug is fixed when the branch is
// recorded, and a later change of the title does not move it.
func (s *Store) WriteTicketBranch(ticket string, branch string) error {
	return s.mutateTicket(ticket, func(t *Ticket) {
		t.Branch = branch
		if t.Slug == "" {
			t.Slug = Slug(t.Title)
		}
	})
}

// TicketFileRelPath returns <ticket>/ticket.yaml relative to the store root:
// the path a Claim that edits the record stages and commits.
func (s *Store) TicketFileRelPath(ticket string) string {
	return filepath.Join(s.TicketRelDir(ticket), "ticket.yaml")
}

// SetTicketRank sets <ticket>/ticket.yaml's rank to *rank, or removes it when
// rank is nil, leaving the rest of the record untouched. Setting a rank
// creates the record when the ticket has none; removing one from a ticket
// with no record writes nothing. It only writes the file: jig rank commits it
// through Claim.
func (s *Store) SetTicketRank(ticket string, rank *int) error {
	if rank == nil {
		if _, err := os.Stat(s.TicketFilePath(ticket)); os.IsNotExist(err) {
			return nil
		}
	}
	return s.mutateTicket(ticket, func(t *Ticket) { t.Rank = rank })
}

// SetMigratedTicket rewrites ticket's whole ticket.yaml as `jig store
// migrate` leaves it: title and body resolved on the v1 store (fonts filled
// with the chart entry's or history's text where ticket.yaml itself had
// none), aliases with oldID appended (deduplicated, so a second migrate
// attempt or a record that already carried it is idempotent), and blockedBy
// translated to the new ids. branch is carried over unchanged - the
// migration renames no branch. It creates the record when ticket has none
// yet (a ticket folder moved with no ticket.yaml of its own).
func (s *Store) SetMigratedTicket(ticket, oldID, title, body string, blockedBy []TicketBlockedBy) error {
	return s.mutateTicket(ticket, func(t *Ticket) {
		t.Title = title
		t.Body = body
		t.BlockedBy = blockedBy
		hasAlias := false
		for _, a := range t.Aliases {
			if a == oldID {
				hasAlias = true
				break
			}
		}
		if !hasAlias {
			t.Aliases = append(t.Aliases, oldID)
		}
	})
}

// TicketBranch returns ticket's working branch, resolved and validated
// against target: the branch recorded in its ticket.yaml, or the default
// name when none is recorded: <id>-<slug>, where id is the ticket's GitHub
// issue number once the mirror has given it one (and the project's branches
// are led by GitHub's ids, Store.BranchesFrom) and the ticket's own id
// before that, and slug is Slug of its title, else of its brief's heading; a
// ticket with no slug to give is named by the bare id. Every caller that names
// a ticket's branch resolves it here instead of building the name itself, so
// a branch the ticket adopted is picked up by every one of them - and every
// one of them gets this same validation for free, rather than each having to
// remember to run it after its own read. A caller that names the branch more
// than once in one command (publish, gate) resolves it once and passes the
// result down, so the whole command works on one branch even if ticket.yaml
// changes under it.
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
// name check. Adoption checks the same rules before it records a name
// (CheckAdoptableBranch), so this also guards a hand-edited or otherwise
// externally written ticket.yaml, which `jig validate` checks the same way.
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
	if !rec.Adopted() {
		return s.defaultBranch(ticket, rec)
	}
	fault := s.branchNameFault(rec.Branch, target)
	if fault == nil {
		return rec.Branch, nil
	}
	recordPath := s.TicketFilePath(ticket)
	switch fault.kind {
	case faultTarget:
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which is also the target branch", ticket, rec.Branch),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{
				fmt.Sprintf("Record a branch other than %q in %s, or remove the branch key to use the default %s", target, recordPath, s.defaultBranchOr(ticket, rec)),
			},
		}
	case faultRejected:
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which git rejects as a branch name: %s", ticket, rec.Branch, fault.detail),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{fmt.Sprintf("Fix the branch key in %s", recordPath)},
		}
	default:
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%s's ticket.yaml records branch %q, which git reads as shorthand for %q, not as a branch name", ticket, rec.Branch, fault.detail),
			Code: "TICKET_BRANCH_INVALID",
			Help: []string{fmt.Sprintf("Record the branch's own name in %s", recordPath)},
		}
	}
}

// CheckAdoptableBranch reports why branch cannot become ticket's adopted
// branch, before anything records it: the rules ResolveTicketBranch applies
// to a recorded name, so a name that passes here is one every later command
// resolves. It reads and writes nothing of the ticket's.
func (s *Store) CheckAdoptableBranch(ticket, branch, target string) error {
	fault := s.branchNameFault(branch, target)
	if fault == nil {
		return nil
	}
	var why string
	switch fault.kind {
	case faultTarget:
		why = "it is the target branch, where a push would land without a pull request"
	case faultRejected:
		why = fmt.Sprintf("git rejects it as a branch name: %s", fault.detail)
	default:
		why = fmt.Sprintf("git reads it as shorthand for %q, not as a branch name", fault.detail)
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("%s cannot adopt branch %q: %s", ticket, branch, why),
		Code: "TICKET_BRANCH_INVALID",
		Help: []string{"Name the branch the work was built on, spelled as git names it, and never the target branch"},
	}
}

// branchFaultKind says which rule a branch name broke.
type branchFaultKind int

const (
	faultTarget    branchFaultKind = iota // the name is the target branch
	faultRejected                         // git refuses it as a ref name
	faultShorthand                        // git expands it to another name
)

// branchFault is one broken rule of a branch name: which, and the detail
// that finishes its message (git's error, or the name git expanded it to;
// empty for the target).
type branchFault struct {
	kind   branchFaultKind
	detail string
}

// branchNameFault checks branch as a name against target and git, returning
// nil when it can be a ticket's working branch: the one check both a
// recorded branch (ResolveTicketBranch) and one about to be adopted
// (CheckAdoptableBranch) pass.
func (s *Store) branchNameFault(branch, target string) *branchFault {
	if branch == target {
		return &branchFault{kind: faultTarget}
	}
	// --branch prints the name it accepted, and expands "@{-N}" against this
	// repo's own reflog: whatever it printed must be the recorded name
	// itself, so a name that is only shorthand for another (which one
	// depends on the store's own checkout history, so on the machine) is
	// refused instead of resolved differently from one machine to the next.
	name, err := gitx.Run(s.Root, "check-ref-format", "--branch", branch)
	if err != nil {
		return &branchFault{kind: faultRejected, detail: err.Error()}
	}
	if name != branch {
		return &branchFault{kind: faultShorthand, detail: name}
	}
	return nil
}

// defaultBranch names the branch of a ticket that records none: <id>-<slug>.
// The id is the one that leads the project's branches (branchID): the
// ticket's GitHub issue number once it is mirrored, else its store id. The
// slug is the ticket's own, or the one its title gives for a ticket minted
// before the field existed. The default is derived, never written to
// ticket.yaml's branch: that key marks an adopted branch.
//
// A ticket with no title to give a slug (none recorded, or one with no ASCII
// word) takes it from the first "# " heading of its brief, without the
// "<id>:" that heading may lead with (briefSlug). Tickets minted through jig
// have a title, so this is a ticket whose folder was made by hand. A ticket
// with no slug from either is named by the bare id: the "jig/<id>" prefix
// went with the branches named for their titles (decision 31), and a
// jig/<id> branch still binds to its ticket (TicketForBranch).
func (s *Store) defaultBranch(ticket string, rec Ticket) (string, error) {
	slug := rec.Slug
	if slug == "" {
		slug = Slug(rec.Title)
	}
	if slug == "" {
		slug = s.briefSlug(ticket, rec)
	}
	lead, err := s.branchID(ticket)
	if err != nil {
		return "", err
	}
	if slug == "" {
		return lead, nil
	}
	return lead + "-" + slug, nil
}

// briefSlug is the slug of ticket's brief heading: the first "# " heading of
// brief.md, with a leading "<id>:" taken off (the id the ticket has or had),
// run through Slug. It is "" when the ticket has no brief, no heading, or a
// heading that is nothing but its id.
func (s *Store) briefSlug(ticket string, rec Ticket) string {
	brief, err := os.ReadFile(filepath.Join(s.TicketDir(ticket), "brief.md"))
	if err != nil {
		return ""
	}
	title := BriefTitle(brief)
	for _, id := range append([]string{ticket}, rec.Aliases...) {
		if len(title) > len(id) && strings.EqualFold(title[:len(id)], id) && title[len(id)] == ':' {
			title = title[len(id)+1:]
			break
		}
	}
	return Slug(title)
}

// defaultBranchOr is defaultBranch for a message that only wants to name the
// default: when the name cannot be worked out, the bare id.
func (s *Store) defaultBranchOr(ticket string, rec Ticket) string {
	if name, err := s.defaultBranch(ticket, rec); err == nil {
		return name
	}
	return ticket
}
