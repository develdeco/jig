package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

func TestTicketDepsRoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := st.ReadTicketDeps("T-1")
	if err != nil {
		t.Fatalf("ReadTicketDeps on an absent file: %v", err)
	}
	if got != nil {
		t.Fatalf("ReadTicketDeps on an absent file = %+v, want nil", got)
	}

	want := []TicketBlockedBy{
		{Ticket: "T-4", Kind: "merged"},
		{Ticket: "T-9", Kind: "stacked"},
	}
	if err := st.CreateTicketRecord("T-1", Ticket{BlockedBy: want}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	got, err = st.ReadTicketDeps("T-1")
	if err != nil {
		t.Fatalf("ReadTicketDeps: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadTicketDeps = %+v, want %+v", got, want)
	}

	if _, err := os.Stat(filepath.Join(st.TicketDir("T-1"), "ticket.yaml")); err != nil {
		t.Fatalf("ticket.yaml not written: %v", err)
	}
}

// TestTicketRecordRoundTrip covers every field of the record together:
// title and blocked_by written whole by CreateTicketRecord, then branch added
// by WriteTicketBranch, all survive a read.
func TestTicketRecordRoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket on an absent file: %v", err)
	}
	if !reflect.DeepEqual(got, Ticket{}) {
		t.Fatalf("ReadTicket on an absent file = %+v, want a zero Ticket", got)
	}

	deps := []TicketBlockedBy{{Ticket: "T-4", Kind: "merged"}}
	if err := st.CreateTicketRecord("T-1", Ticket{Title: "Fix the thing", Body: "Line one.\nLine two.", BlockedBy: deps}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.WriteTicketBranch("T-1", "fix/T-1"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	got, err = st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	// Recording the branch also fixed the slug the title gives (the ticket
	// was minted without one).
	want := Ticket{Title: "Fix the thing", Body: "Line one.\nLine two.", Slug: "fix-the-thing", Branch: "fix/T-1", BlockedBy: deps}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadTicket = %+v, want %+v", got, want)
	}
}

// TestTicketBodyWrittenAsLiteralBlockAndKeptByRewrite covers the body's wire
// shape (a literal block, "body: |", next to title - the same style
// ChartEntry.Body is written in, charts.go) and the rule every rewrite of
// the record follows: WriteTicketBranch's own read-modify-write must not
// drop a body it does not otherwise touch, even a single-line one, which
// yaml.v3 would otherwise write in plain style on its own. An empty body
// writes no key at all.
func TestTicketBodyWrittenAsLiteralBlockAndKeptByRewrite(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("T-1", Ticket{Title: "Fix the thing", Body: "A single line"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	raw, err := os.ReadFile(st.TicketFilePath("T-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "body: |") {
		t.Fatalf("ticket.yaml does not write body as a literal block:\n%s", raw)
	}

	if err := st.WriteTicketBranch("T-1", "fix/T-1"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Body != "A single line" {
		t.Fatalf("body after WriteTicketBranch = %q, want %q (a rewrite must keep it)", got.Body, "A single line")
	}

	if err := st.CreateTicketRecord("T-2", Ticket{Title: "Nothing to say"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	raw, err = os.ReadFile(st.TicketFilePath("T-2"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "body:") {
		t.Fatalf("ticket.yaml writes a body key for an empty body:\n%s", raw)
	}
}

// TestWriteTicketBranchPreservesTitleAndDeps pins the fix for the exact bug a
// full-file replace had: writing the branch alone must not drop a title or
// blockers that were already recorded. It also covers the record's first
// write: a ticket with no ticket.yaml gets one holding just the branch.
func TestWriteTicketBranchPreservesTitleAndDeps(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	deps := []TicketBlockedBy{{Ticket: "T-4", Kind: "merged"}}
	if err := st.CreateTicketRecord("T-1", Ticket{Title: "Fix the thing", BlockedBy: deps}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	if err := st.WriteTicketBranch("T-1", "fix/T-1"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("title after WriteTicketBranch = %q, want %q", got.Title, "Fix the thing")
	}
	if !reflect.DeepEqual(got.BlockedBy, deps) {
		t.Fatalf("blocked_by after WriteTicketBranch = %+v, want %+v", got.BlockedBy, deps)
	}

	if err := st.WriteTicketBranch("T-2", "fix/T-2"); err != nil {
		t.Fatalf("WriteTicketBranch on a ticket with no record: %v", err)
	}
	if got, err := st.ReadTicket("T-2"); err != nil || !reflect.DeepEqual(got, Ticket{Branch: "fix/T-2"}) {
		t.Fatalf("ReadTicket after the first write = %+v, %v, want just the branch", got, err)
	}
}

// TestReadTicketRefusesUnknownField pins the decision that an unknown
// ticket.yaml key is refused rather than silently dropped on the next
// rewrite: a field this jig version does not know about (a newer jig, or a
// hand-edit typo) must fail the read loudly instead of quietly vanishing
// the next time WriteTicketBranch rewrites the file. The
// refusal is an *axi.Error whose help names both causes, since the read
// cannot tell them apart: upgrading jig is the fix for a key a newer jig
// wrote, editing the file is the fix for a typo.
func TestReadTicketRefusesUnknownField(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	content := "schema_version: 1\ntitle: Widget\nfrobnicate: yes\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.ReadTicket("T-1")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_RECORD_INVALID" {
		t.Fatalf("ReadTicket with an unknown field = %v, want *axi.Error TICKET_RECORD_INVALID", err)
	}
	help := strings.Join(ae.Help, "\n")
	if !strings.Contains(help, "upgrade jig") {
		t.Fatalf("help = %q, want it to name upgrading jig (a newer jig may have written the key)", help)
	}
	if !strings.Contains(help, path) {
		t.Fatalf("help = %q, want it to name %s (a typo is fixed there)", help, path)
	}
}

// TestReadTicketRefusesNewerSchemaVersion pins the other half of the same
// decision: a schema_version newer than this jig understands is refused,
// not silently accepted and later downgraded back to ticketSchemaVersion
// the next time something rewrites the file - a future jig could repurpose
// an existing key's meaning under a new schema_version without adding a
// field KnownFields would catch.
func TestReadTicketRefusesNewerSchemaVersion(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	content := "schema_version: 2\ntitle: From a newer jig\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.ReadTicket("T-1")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_SCHEMA_UNSUPPORTED" {
		t.Fatalf("ReadTicket with schema_version 2 = %v, want *axi.Error TICKET_SCHEMA_UNSUPPORTED", err)
	}

	if err := st.WriteTicketBranch("T-1", "feature/x"); err == nil {
		t.Fatal("WriteTicketBranch on a newer-schema ticket.yaml: want an error, got nil (would downgrade schema_version on rewrite)")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content {
		t.Fatalf("ticket.yaml changed after a refused write:\nbefore %q\nafter  %q", content, after)
	}
}

// TestTicketMutateConcurrentWritersSurviveTheLock covers mutateTicket's
// store lock: eight writers each add their own blocker to the same ticket
// while WriteTicketBranch sets its branch, all concurrently, and every one
// must survive, since each is a read-modify-write over the whole record.
// Without the lock, two of them can interleave their reads before either
// writes, so the second write's read-modify-write is built on a stale copy
// and clobbers the first writer's change on save - a lost update.
func TestTicketMutateConcurrentWritersSurviveTheLock(t *testing.T) {
	const writers = 8
	st := &Store{Root: t.TempDir()}

	var wg sync.WaitGroup
	wg.Add(writers + 1)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			blocker := TicketBlockedBy{Ticket: fmt.Sprintf("T-%d", i+2), Kind: "merged"}
			err := st.mutateTicket("T-1", func(tk *Ticket) { tk.BlockedBy = append(tk.BlockedBy, blocker) })
			if err != nil {
				t.Errorf("mutateTicket: %v", err)
			}
		}()
	}
	go func() {
		defer wg.Done()
		if err := st.WriteTicketBranch("T-1", "feature/x"); err != nil {
			t.Errorf("WriteTicketBranch: %v", err)
		}
	}()
	wg.Wait()

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feature/x" || len(got.BlockedBy) != writers {
		t.Fatalf("ticket after %d concurrent writers = %+v, want the branch and all %d blockers (lost update)", writers+1, got, writers)
	}
}

// TestTicketMutateRefusesUnreadableRecord covers mutateTicket's other rule
// that survives mutation alongside the lock: a caller must never get the
// chance to rewrite, and so lose the unknown key from, a ticket.yaml it
// cannot read. WriteTicketBranch refuses, and ticket.yaml's bytes are
// unchanged after the refusal.
func TestTicketMutateRefusesUnreadableRecord(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	before := []byte("schema_version: 1\ntitle: Widget\nfrobnicate: yes\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := st.WriteTicketBranch("T-1", "feature/x"); err == nil {
		t.Fatal("WriteTicketBranch on an unreadable ticket.yaml: want an error, got nil")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ticket.yaml changed after the refused write:\nbefore %q\nafter  %q", before, after)
	}
}

// TestTicketBranchDefault covers a ticket with no ticket.yaml at all, which
// has no title to name a branch for and is named by its bare id, and one whose
// ticket.yaml exists but records no branch (title/blocked_by only), which
// resolves to "<ticket>-<slug of its title>".
func TestTicketBranchDefault(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	got, err := st.TicketBranch("T-1", "main")
	if err != nil {
		t.Fatalf("TicketBranch on an absent ticket.yaml: %v", err)
	}
	if got != "T-1" {
		t.Fatalf("TicketBranch = %q, want %q", got, "T-1")
	}

	if err := st.CreateTicketRecord("T-2", Ticket{Title: "Fix the thing"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	got, err = st.TicketBranch("T-2", "main")
	if err != nil {
		t.Fatalf("TicketBranch with a title but no branch: %v", err)
	}
	if got != "T-2-fix-the-thing" {
		t.Fatalf("TicketBranch = %q, want %q", got, "T-2-fix-the-thing")
	}
}

// writeBrief writes brief.md in ticket's folder of st, as a person or an
// agent that made the folder by hand would: no ticket.yaml beside it.
func writeBrief(t *testing.T, st *Store, ticket, text string) {
	t.Helper()
	dir := st.TicketDir(ticket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBranchFromBriefHeadingWhenNoTitle covers a ticket whose folder was made by
// hand: with no title (no record, or a record that has none) its branch takes
// its slug from the brief's first "# " heading, without the "<id>:" the heading
// leads with. A recorded title still wins over the heading, and a "## "
// heading is a section, never the title.
func TestBranchFromBriefHeadingWhenNoTitle(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, tc := range []struct {
		name, ticket, brief string
		rec                 *Ticket
		want                string
	}{
		{"the id and a colon lead the heading", "SHOP-1", "# SHOP-1: Show the cart total\n\n## Goal\n\nx\n", nil, "SHOP-1-show-the-cart-total"},
		{"a heading with no id", "SHOP-2", "# Cart discount\n\n## Goal\n\nx\n", nil, "SHOP-2-cart-discount"},
		{"the id in another case", "SHOP-3", "# shop-3:   Empty cart message\n", nil, "SHOP-3-empty-cart-message"},
		{"an id the ticket carried before", "SHOP-4", "# OLD-7: Wishlist\n", &Ticket{Aliases: []string{"OLD-7"}}, "SHOP-4-wishlist"},
		{"a record with no title", "SHOP-5", "# SHOP-5: Gift wrap\n", &Ticket{Slug: ""}, "SHOP-5-gift-wrap"},
		{"the title wins", "SHOP-6", "# SHOP-6: Gift wrap\n", &Ticket{Title: "Returns page"}, "SHOP-6-returns-page"},
		{"the first heading wins", "SHOP-7", "intro\n\n# SHOP-7: First\n\n# Second\n", nil, "SHOP-7-first"},
		{"a section is not the title", "SHOP-8", "## Goal\n\nx\n", nil, "SHOP-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeBrief(t, st, tc.ticket, tc.brief)
			if tc.rec != nil {
				if err := st.CreateTicketRecord(tc.ticket, *tc.rec); err != nil {
					t.Fatalf("CreateTicketRecord: %v", err)
				}
			}
			got, err := st.TicketBranch(tc.ticket, "main")
			if err != nil || got != tc.want {
				t.Fatalf("TicketBranch = %q, %v, want %q", got, err, tc.want)
			}
			// The branch is the default, never an adopted one, and binds back.
			if rec, err := st.ReadTicket(tc.ticket); err != nil || rec.Adopted() {
				t.Fatalf("ReadTicket = %+v, %v, want an unadopted ticket", rec, err)
			}
		})
	}
}

// TestBranchIsTheBareIdWithNoTitleAtAll covers a ticket with no slug from its
// record or its brief (no folder, no brief, a brief with no heading, a heading
// that is only the id, or a title with no ASCII word): its branch is its bare
// id, not the retired "jig/<id>"; a jig/<id> branch still binds to the ticket.
func TestBranchIsTheBareIdWithNoTitleAtAll(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	writeBrief(t, st, "T-2", "## Goal\n\nx\n")
	writeBrief(t, st, "T-3", "# T-3:\n\n## Goal\n\nx\n")
	writeBrief(t, st, "T-4", "# 日本語\n")
	for _, ticket := range []string{"T-1", "T-2", "T-3", "T-4"} {
		got, err := st.TicketBranch(ticket, "main")
		if err != nil || got != ticket {
			t.Errorf("TicketBranch(%s) = %q, %v, want the bare id", ticket, got, err)
		}
		if got := BranchLeadID("jig/" + ticket); got != ticket {
			t.Errorf("BranchLeadID(jig/%s) = %q, want %s: a jig/<id> branch still binds", ticket, got, ticket)
		}
	}
}

// TestTicketBranchRecorded covers a ticket with a recorded branch: it wins
// over the "<ticket>-<slug>" default.
func TestTicketBranchRecorded(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.WriteTicketBranch("T-1", "feature/custom"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	got, err := st.TicketBranch("T-1", "main")
	if err != nil {
		t.Fatalf("TicketBranch: %v", err)
	}
	if got != "feature/custom" {
		t.Fatalf("TicketBranch = %q, want %q", got, "feature/custom")
	}
}

// TestResolveTicketBranchWorksOnTheRecordItIsGiven covers the caller that has
// already read the record for something else (publish's title): the branch
// comes from the record it hands over, default and refusals included, not from
// a second read of ticket.yaml, which here records another branch that would
// pass every check.
func TestResolveTicketBranchWorksOnTheRecordItIsGiven(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.WriteTicketBranch("T-1", "on-disk"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	got, err := st.ResolveTicketBranch("T-1", Ticket{Branch: "given"}, "main")
	if err != nil || got != "given" {
		t.Fatalf("ResolveTicketBranch(a record with a branch) = %q, %v, want %q", got, err, "given")
	}
	got, err = st.ResolveTicketBranch("T-1", Ticket{}, "main")
	if err != nil || got != "T-1" {
		t.Fatalf("ResolveTicketBranch(a record with none) = %q, %v, want the default %q", got, err, "T-1")
	}
	_, err = st.ResolveTicketBranch("T-1", Ticket{Branch: "main"}, "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID" {
		t.Fatalf("ResolveTicketBranch(the target) = %v, want *axi.Error TICKET_BRANCH_INVALID", err)
	}
}

// TestTicketBranchRefusesTarget covers the guard reconcile and publish both
// rely on: a recorded branch equal to target is never returned, since
// trusting it would let a hand-edited or externally written ticket.yaml
// land a merge or a push straight onto target instead of through a PR.
func TestTicketBranchRefusesTarget(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.WriteTicketBranch("T-1", "main"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	_, err := st.TicketBranch("T-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID" {
		t.Fatalf("TicketBranch(target) = %v, want *axi.Error TICKET_BRANCH_INVALID", err)
	}
}

// TestTicketBranchRefusesBadRefName covers the other half of the same
// guard: a recorded branch git itself would refuse as a ref name is never
// returned either, so the failure surfaces here instead of deep inside a
// later git command.
func TestTicketBranchRefusesBadRefName(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.WriteTicketBranch("T-1", "bad..name"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	_, err := st.TicketBranch("T-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID" {
		t.Fatalf("TicketBranch(bad ref) = %v, want *axi.Error TICKET_BRANCH_INVALID", err)
	}
}

// TestTicketBranchRefusesShorthandForAnotherBranch covers a name git accepts
// but expands: check-ref-format --branch reads "@{-1}" as the previously
// checked-out branch of the repo it runs in, so the store's own checkout
// history would decide what a recorded "@{-1}" means, and jig validate could
// pass on one machine and fail on another for the same store content. The
// store here has a previous checkout, so the expansion succeeds and only a
// comparison of the printed name with the recorded one refuses it.
func TestTicketBranchRefusesShorthandForAnotherBranch(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	runGit(t, work, "checkout", "-b", "other")
	runGit(t, work, "checkout", "main")
	if got := strings.TrimSpace(runGit(t, work, "check-ref-format", "--branch", "@{-1}")); got != "other" {
		t.Fatalf("test setup: git expands @{-1} to %q, want %q", got, "other")
	}
	if err := st.WriteTicketBranch("T-1", "@{-1}"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	got, err := st.TicketBranch("T-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID" {
		t.Fatalf("TicketBranch(%q) = %q, %v, want *axi.Error TICKET_BRANCH_INVALID", "@{-1}", got, err)
	}
	if !strings.Contains(ae.Msg, `"other"`) {
		t.Fatalf("Msg = %q, want it to name the branch git expands @{-1} to", ae.Msg)
	}
}

// TestTicketBranchSurfacesUnreadableRecord covers the hot path every build
// lease, gate round and publish resolves its branch through: a ticket.yaml
// with an unknown key (a hand edit, or one written by a newer jig sharing
// the store) fails with the *axi.Error ReadTicket built, its code and help
// intact, not a bare message that names no next step.
func TestTicketBranchSurfacesUnreadableRecord(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	content := "schema_version: 1\ntitle: Widget\nnote: from a newer jig\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.TicketBranch("T-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_RECORD_INVALID" {
		t.Fatalf("TicketBranch on an unreadable ticket.yaml = %v, want *axi.Error TICKET_RECORD_INVALID", err)
	}
	if len(ae.Help) == 0 {
		t.Fatalf("TicketBranch on an unreadable ticket.yaml: want Help naming what to do, got none")
	}
}

// TestTicketBranchSurfacesNewerSchemaVersion covers the other refusal on
// the same path: a ticket.yaml written under a newer schema_version keeps
// its own code and its "upgrade jig" help when it fails a build lease, gate
// round or publish, instead of being folded into a generic record error
// whose help tells the operator to fix or remove another jig's data.
func TestTicketBranchSurfacesNewerSchemaVersion(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 2\nbranch: feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.TicketBranch("T-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_SCHEMA_UNSUPPORTED" {
		t.Fatalf("TicketBranch on a schema_version 2 ticket.yaml = %v, want *axi.Error TICKET_SCHEMA_UNSUPPORTED", err)
	}
	if help := strings.Join(ae.Help, "\n"); !strings.Contains(help, "Upgrade jig") {
		t.Fatalf("help = %q, want it to name upgrading jig", help)
	}
}

// TestCreateTicketRecordWritesEveryFieldInOneWrite covers the mint-time
// write: title and blocked_by land together, and a read returns exactly
// what was created.
func TestCreateTicketRecordWritesEveryFieldInOneWrite(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	want := Ticket{
		Title:     "Fix the thing",
		Body:      "Why this matters and what to do about it.",
		BlockedBy: []TicketBlockedBy{{Ticket: "T-4", Kind: "merged"}, {Ticket: "T-9", Kind: "stacked"}},
	}

	if err := st.CreateTicketRecord("T-1", want); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadTicket = %+v, want %+v", got, want)
	}
}

// TestCreateTicketRecordRefusesAnExistingRecord covers the collision rule:
// a ticket that already has a ticket.yaml is never merged into, however the
// existing record got there, and its bytes are unchanged after the refusal.
// The refusal wraps ErrTicketRecordExists so a caller can tell it apart from
// any other write failure.
func TestCreateTicketRecordRefusesAnExistingRecord(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	seed := Ticket{Title: "Pre-existing, unrelated", BlockedBy: []TicketBlockedBy{{Ticket: "T-9", Kind: "merged"}}}
	if err := st.CreateTicketRecord("T-1", seed); err != nil {
		t.Fatalf("seed the existing record: %v", err)
	}
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = st.CreateTicketRecord("T-1", Ticket{Title: "New chart entry"})
	if !errors.Is(err, ErrTicketRecordExists) {
		t.Fatalf("CreateTicketRecord over an existing record = %v, want it to wrap ErrTicketRecordExists", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ticket.yaml changed after a refused create:\nbefore %q\nafter  %q", before, after)
	}
}

// TestCreateTicketRecordConcurrentCreatorsOneWins covers the lock around
// the existence check: however many callers race to create the same
// ticket's record, exactly one succeeds and every other one is refused.
// Without the lock two of them can both see no file before either writes,
// and both succeed, the second silently replacing the first.
func TestCreateTicketRecordConcurrentCreatorsOneWins(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	const creators = 8
	errs := make([]error, creators)
	var wg sync.WaitGroup
	wg.Add(creators)
	for i := range errs {
		go func() {
			defer wg.Done()
			errs[i] = st.CreateTicketRecord("T-1", Ticket{Title: fmt.Sprintf("creator %d", i)})
		}()
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrTicketRecordExists):
			t.Fatalf("creator %d: %v, want success or ErrTicketRecordExists", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent creators succeeded, want exactly 1", won, creators)
	}
}

// TestCheckAdoptableBranch: a name passes the check exactly when TicketBranch
// would resolve it once recorded - the target, a name git rejects, and one
// git expands to another are refused with TICKET_BRANCH_INVALID, each saying
// why - and the check writes nothing: no ticket.yaml appears.
func TestCheckAdoptableBranch(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	runGit(t, work, "checkout", "-b", "other")
	runGit(t, work, "checkout", "main")

	for _, tc := range []struct {
		branch  string
		refused bool
		mention string
	}{
		{"add-retry", false, ""},
		{"feature/add-retry", false, ""},
		{"main", true, "target branch"},
		{"bad..name", true, "git rejects"},
		{"@{-1}", true, `"other"`},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			err := st.CheckAdoptableBranch("T-1", tc.branch, "main")
			var ae *axi.Error
			switch {
			case !tc.refused && err != nil:
				t.Fatalf("CheckAdoptableBranch(%q) = %v, want it adoptable", tc.branch, err)
			case tc.refused && (!errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID"):
				t.Fatalf("CheckAdoptableBranch(%q) = %v, want *axi.Error TICKET_BRANCH_INVALID", tc.branch, err)
			case tc.refused && (!strings.Contains(ae.Msg, tc.mention) || !strings.Contains(ae.Msg, "T-1 cannot adopt branch")):
				t.Fatalf("Msg = %q, want it to say T-1 cannot adopt the branch, and %q", ae.Msg, tc.mention)
			}

			// The same verdict TicketBranch gives the name once recorded.
			if err := st.WriteTicketBranch("T-2", tc.branch); err != nil {
				t.Fatalf("WriteTicketBranch: %v", err)
			}
			_, resolveErr := st.TicketBranch("T-2", "main")
			if (resolveErr != nil) != tc.refused {
				t.Fatalf("TicketBranch(%q) = %v, but CheckAdoptableBranch refused = %v: one rule must decide both", tc.branch, resolveErr, tc.refused)
			}
		})
	}
	if _, err := os.Stat(st.TicketFilePath("T-1")); !os.IsNotExist(err) {
		t.Fatalf("CheckAdoptableBranch wrote T-1's ticket.yaml (stat err %v)", err)
	}
}

// TestStartSHARoundTrip: the start sha is one file per repo under the ticket,
// written whole, replacing an earlier one.
func TestStartSHARoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	want := filepath.Join(st.TicketDir("T-1"), "start.fixture-repo.sha")
	if got := st.StartSHAPath("T-1", "fixture-repo"); got != want {
		t.Fatalf("StartSHAPath = %q, want %q", got, want)
	}

	for _, sha := range []string{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"} {
		if err := st.WriteStartSHA("T-1", "fixture-repo", sha); err != nil {
			t.Fatalf("WriteStartSHA: %v", err)
		}
		if got, err := os.ReadFile(want); err != nil || string(got) != sha {
			t.Fatalf("start sha file = %q (err %v), want %q", got, err, sha)
		}
	}
}

// TestTicketAdoptedIsTheRecordedBranch: a ticket adopted a branch exactly when
// its record holds one, whatever else the record holds.
func TestTicketAdoptedIsTheRecordedBranch(t *testing.T) {
	for _, tc := range []struct {
		rec  Ticket
		want bool
	}{
		{Ticket{}, false},
		{Ticket{Title: "Add retry", BlockedBy: []TicketBlockedBy{{Ticket: "T-1", Kind: "hard"}}}, false},
		{Ticket{Branch: "add-retry"}, true},
		{Ticket{Title: "Add retry", Branch: "add-retry"}, true},
	} {
		if got := tc.rec.Adopted(); got != tc.want {
			t.Errorf("%+v.Adopted() = %v, want %v", tc.rec, got, tc.want)
		}
	}
}

// TestSetTicketRank covers the rank field of the record: it is written and
// read back, a rewrite of the record for any other field keeps it, setting it
// keeps the rest of the record, and clearing it leaves no key behind.
func TestSetTicketRank(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	deps := []TicketBlockedBy{{Ticket: "T-4", Kind: "merged"}}
	if err := st.CreateTicketRecord("T-1", Ticket{Title: "Fix the thing", BlockedBy: deps}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if got, _ := st.ReadTicket("T-1"); got.Rank != nil {
		t.Fatalf("rank of a ticket minted without one = %d, want none", *got.Rank)
	}

	three := 3
	if err := st.SetTicketRank("T-1", &three); err != nil {
		t.Fatalf("SetTicketRank: %v", err)
	}
	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Rank == nil || *got.Rank != 3 || got.Title != "Fix the thing" || !reflect.DeepEqual(got.BlockedBy, deps) {
		t.Fatalf("ReadTicket after SetTicketRank = %+v, want rank 3 with the title and blockers kept", got)
	}
	data, err := os.ReadFile(st.TicketFilePath("T-1"))
	if err != nil || !strings.Contains(string(data), "\nrank: 3\n") {
		t.Fatalf("ticket.yaml = %q, %v, want a rank: 3 line", data, err)
	}

	// A rewrite for another field keeps the rank, and rank 0 is a rank.
	if err := st.WriteTicketBranch("T-1", "fix/T-1"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
	if got, _ := st.ReadTicket("T-1"); got.Rank == nil || *got.Rank != 3 {
		t.Fatalf("rank after WriteTicketBranch = %v, want 3", got.Rank)
	}
	zero := 0
	if err := st.SetTicketRank("T-1", &zero); err != nil {
		t.Fatalf("SetTicketRank 0: %v", err)
	}
	if got, _ := st.ReadTicket("T-1"); got.Rank == nil || *got.Rank != 0 {
		t.Fatalf("rank after SetTicketRank 0 = %v, want 0", got.Rank)
	}

	if err := st.SetTicketRank("T-1", nil); err != nil {
		t.Fatalf("SetTicketRank nil: %v", err)
	}
	got, err = st.ReadTicket("T-1")
	if err != nil || got.Rank != nil || got.Title != "Fix the thing" || got.Branch != "fix/T-1" {
		t.Fatalf("ReadTicket after clearing = %+v, %v, want no rank and the rest kept", got, err)
	}
	if data, _ := os.ReadFile(st.TicketFilePath("T-1")); strings.Contains(string(data), "rank") {
		t.Fatalf("ticket.yaml after clearing = %q, want no rank key", data)
	}
}

// TestSetTicketRankOnATicketWithNoRecord: setting a rank creates the record,
// clearing one writes nothing.
func TestSetTicketRankOnATicketWithNoRecord(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTicketRank("T-1", nil); err != nil {
		t.Fatalf("SetTicketRank nil: %v", err)
	}
	if _, err := os.Stat(st.TicketFilePath("T-1")); !os.IsNotExist(err) {
		t.Fatalf("clearing the rank of a ticket with no record wrote ticket.yaml (stat err = %v)", err)
	}

	two := 2
	if err := st.SetTicketRank("T-1", &two); err != nil {
		t.Fatalf("SetTicketRank: %v", err)
	}
	if got, err := st.ReadTicket("T-1"); err != nil || got.Rank == nil || *got.Rank != 2 {
		t.Fatalf("ReadTicket = %+v, %v, want rank 2", got, err)
	}
}

// TestReadTicketRefusesARankThatIsNotAnInteger: the key is known, so only its
// value can be wrong, and the refusal is the one every unreadable record gets.
func TestReadTicketRefusesARankThatIsNotAnInteger(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TicketFilePath("T-1"), []byte("schema_version: 1\nrank: soon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReadTicket("T-1")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_RECORD_INVALID" {
		t.Fatalf("ReadTicket of rank: soon = %v, want TICKET_RECORD_INVALID", err)
	}
}

// TestTicketFileRelPath: the record's path under the store root, the one a
// claim stages.
func TestTicketFileRelPath(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if got, want := st.TicketFileRelPath("T-1"), filepath.Join("tickets", "T-1", "ticket.yaml"); got != want {
		t.Fatalf("TicketFileRelPath = %q, want %q", got, want)
	}
}
