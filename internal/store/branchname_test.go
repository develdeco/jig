package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// mirrorTo records, as the GitHub mirror does, that ticket's issue is number
// issue of repo.
func mirrorTo(t *testing.T, st *Store, ticket, repo string, issue int) {
	t.Helper()
	writeTrackerRecord(t, st, ticket, fmt.Sprintf("# This file is written by jig's GitHub mirror; edits here are overwritten.\nrepo: %s\nissue: %d\nnode_id: I_%d\n", repo, issue, issue))
}

// writeTrackerRecord writes ticket's tracker/github.yaml as given.
func writeTrackerRecord(t *testing.T, st *Store, ticket, content string) {
	t.Helper()
	path := st.githubRecordPath(ticket)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTicketBranchIsTheIDAndTheSlugOfTheTitle: with no tracker a ticket's
// branch is its store id, a dash and the slug of its title - recorded at mint,
// else derived from the title - and the default is never written to the
// record, so the ticket is not adopted.
func TestTicketBranchIsTheIDAndTheSlugOfTheTitle(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for id, rec := range map[string]Ticket{
		"JIG-1": {Title: "Upload item gallery", Slug: "gallery"},
		"JIG-2": {Title: "Fix the login page"},
		"JIG-3": {Title: "Upload item gallery", Slug: "chosen-slug"},
	} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{
		"JIG-1": "JIG-1-gallery",            // the recorded slug, not the title's
		"JIG-2": "JIG-2-fix-the-login-page", // an older ticket: derived on read
		"JIG-3": "JIG-3-chosen-slug",
	} {
		got, err := st.TicketBranch(id, "main")
		if err != nil || got != want {
			t.Errorf("TicketBranch(%s) = %q, %v; want %q", id, got, err, want)
		}
		rec, err := st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Adopted() || rec.Branch != "" {
			t.Errorf("%s: after naming its branch the record holds branch %q: the default must not be written", id, rec.Branch)
		}
	}
}

// TestTicketBranchKeepsItsSlugWhenTheTitleChanges: the slug is fixed at mint, so
// editing the title does not rename the branch; a ticket with no slug of its
// own follows its title until a branch is recorded, which fixes it.
func TestTicketBranchKeepsItsSlugWhenTheTitleChanges(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "First title", Slug: Slug("First title")}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-2", Ticket{Title: "First title"}); err != nil {
		t.Fatal(err)
	}
	retitle := func(id string) {
		t.Helper()
		if err := st.mutateTicket(id, func(r *Ticket) { r.Title = "A different title" }); err != nil {
			t.Fatal(err)
		}
	}
	retitle("JIG-1")
	retitle("JIG-2")
	if got, _ := st.TicketBranch("JIG-1", "main"); got != "JIG-1-first-title" {
		t.Errorf("a ticket minted with its slug: TicketBranch = %q after a retitle, want JIG-1-first-title", got)
	}
	if got, _ := st.TicketBranch("JIG-2", "main"); got != "JIG-2-a-different-title" {
		t.Errorf("a ticket with no slug: TicketBranch = %q after a retitle, want it derived from the title now", got)
	}

	// Recording a branch fixes the slug it would have had.
	if err := st.WriteTicketBranch("JIG-2", "work/gallery"); err != nil {
		t.Fatal(err)
	}
	rec, err := st.ReadTicket("JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Slug != "a-different-title" || rec.Branch != "work/gallery" {
		t.Errorf("after WriteTicketBranch the record has slug %q, branch %q; want a-different-title, work/gallery", rec.Slug, rec.Branch)
	}
}

// TestTicketBranchIsLedByTheIssueNumberOnceMirrored: a mirrored ticket's branch
// leads with its GitHub issue number, the shape GitHub's own "create a branch"
// gives; before the mirror has given it one, and when the project's jig.yaml
// says another tracker's ids lead branches, it leads with the store id.
func TestTicketBranchIsLedByTheIssueNumberOnceMirrored(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-1", "JIG-2"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: "Upload item gallery"}); err != nil {
			t.Fatal(err)
		}
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 102)

	for _, tc := range []struct {
		name, from, ticket, want string
	}{
		{"mirrored", "", "JIG-1", "102-upload-item-gallery"},
		{"mirrored, GitHub named", "github", "JIG-1", "102-upload-item-gallery"},
		{"mirrored, GitHub named in capitals", " GitHub ", "JIG-1", "102-upload-item-gallery"},
		{"not mirrored yet", "", "JIG-2", "JIG-2-upload-item-gallery"},
		{"mirrored, another tracker leads", "jira", "JIG-1", "JIG-1-upload-item-gallery"},
		{"mirrored, the store leads", "store", "JIG-1", "JIG-1-upload-item-gallery"},
	} {
		st.BranchesFrom = tc.from
		if got, err := st.TicketBranch(tc.ticket, "main"); err != nil || got != tc.want {
			t.Errorf("%s: TicketBranch(%s) = %q, %v; want %q", tc.name, tc.ticket, got, err, tc.want)
		}
	}
	st.BranchesFrom = ""
	if !st.BranchesLedByGitHub() {
		t.Error("BranchesLedByGitHub with no tracker named = false, want true")
	}
	st.BranchesFrom = "jira"
	if st.BranchesLedByGitHub() {
		t.Error("BranchesLedByGitHub with jira named = true, want false")
	}
}

// TestTicketBranchOfATicketWithNothingToSayIsItsBareId: a ticket whose title
// gives no slug, and that has no brief heading to give one, has no tail to put
// after an id, and is named by the id that leads its branches: the GitHub issue
// number once it is mirrored, else its own id. The "jig/" prefix is retired.
func TestTicketBranchOfATicketWithNothingToSayIsItsBareId(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for id, rec := range map[string]Ticket{"JIG-1": {}, "JIG-2": {Title: "日本語"}, "JIG-3": {Title: "???"}} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 7)
	for id, want := range map[string]string{"JIG-1": "7", "JIG-2": "JIG-2", "JIG-3": "JIG-3"} {
		if got, err := st.TicketBranch(id, "main"); err != nil || got != want {
			t.Errorf("TicketBranch(%s) = %q, %v; want %s", id, got, err, want)
		}
	}
	// A ticket with no ticket.yaml at all reads as a zero record.
	if got, err := st.TicketBranch("JIG-9", "main"); err != nil || got != "JIG-9" {
		t.Errorf("TicketBranch(JIG-9), no record = %q, %v; want JIG-9", got, err)
	}
}

// TestTicketBranchRecordedWins: a recorded branch is the ticket's, whatever the
// slug or the issue number would have made, and it is still validated.
func TestTicketBranchRecordedWins(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "Upload item gallery", Slug: "gallery", Branch: "feature/gallery"}); err != nil {
		t.Fatal(err)
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 102)
	if got, err := st.TicketBranch("JIG-1", "main"); err != nil || got != "feature/gallery" {
		t.Errorf("TicketBranch = %q, %v; want the recorded feature/gallery", got, err)
	}
	if err := st.WriteTicketBranch("JIG-1", "main"); err != nil {
		t.Fatal(err)
	}
	_, err := st.TicketBranch("JIG-1", "main")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_BRANCH_INVALID" {
		t.Fatalf("TicketBranch with the target recorded = %v, want TICKET_BRANCH_INVALID", err)
	}
	// The refusal names the name the ticket has without the record.
	if len(ae.Help) == 0 || !strings.Contains(ae.Help[0], "default 102-gallery") {
		t.Errorf("TICKET_BRANCH_INVALID help = %v, want it to name the default 102-gallery", ae.Help)
	}
}

// TestTicketSlugSurvivesEveryRewrite: slug: is part of the record at all four
// places the wire shape is spelled, so a rewrite of the record for another
// field (the branch, a migration's alias) never drops it.
func TestTicketSlugSurvivesEveryRewrite(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	want := Ticket{Title: "Upload item gallery", Slug: "gallery", Body: "text", BlockedBy: []TicketBlockedBy{{Ticket: "JIG-9", Kind: "merged"}}}
	if err := st.CreateTicketRecord("JIG-1", want); err != nil {
		t.Fatal(err)
	}
	got, err := st.ReadTicket("JIG-1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadTicket = %+v, %v; want %+v", got, err, want)
	}
	if err := st.SetMigratedTicket("JIG-1", "T-1", "Upload item gallery", "text", want.BlockedBy); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTicketBranch("JIG-1", "feature/gallery"); err != nil {
		t.Fatal(err)
	}
	got, err = st.ReadTicket("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "gallery" || got.Branch != "feature/gallery" || !reflect.DeepEqual(got.Aliases, []string{"T-1"}) {
		t.Errorf("after the rewrites the record is %+v, want slug gallery, branch feature/gallery and alias T-1", got)
	}
	data, err := os.ReadFile(st.TicketFilePath("JIG-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nslug: gallery\n") {
		t.Errorf("ticket.yaml lacks a slug: key:\n%s", data)
	}
	// A ticket with no slug writes none.
	if err := st.CreateTicketRecord("JIG-2", Ticket{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(st.TicketFilePath("JIG-2")); strings.Contains(string(data), "slug") {
		t.Errorf("a record with no slug wrote one:\n%s", data)
	}
}

// TestTicketForBranchBindsAnIssueNumber: the branch GitHub makes from an issue
// leads with the issue's number, and binds to the ticket the issue mirrors with
// no claim and no recorded branch.
func TestTicketForBranchBindsAnIssueNumber(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-1", "JIG-2", "JIG-3"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 102)
	mirrorTo(t, st, "JIG-2", "acme/tracking", 1020)

	for _, tc := range []struct {
		branch, wantID, wantHow string
	}{
		{"102-upload-item-gallery", "JIG-1", "remote"},
		{"jig/102-upload-item-gallery", "JIG-1", "remote"},
		{"102", "JIG-1", "remote"},
		{"1020-other", "JIG-2", "remote"}, // 102 is not 1020: the whole number leads
		{"JIG-3-no-issue", "JIG-3", "id"},
		// Nothing binds these.
		{"103-unmirrored", "", ""},
		{"102x", "", ""},
		{"0102-leading-zero", "", ""},
		{"feature/102-upload", "", ""},
		{"main", "", ""},
	} {
		id, how, err := st.TicketForBranch(tc.branch)
		if err != nil {
			t.Errorf("TicketForBranch(%q): %v", tc.branch, err)
			continue
		}
		if id != tc.wantID || how != tc.wantHow {
			t.Errorf("TicketForBranch(%q) = %q, %q; want %q, %q", tc.branch, id, how, tc.wantID, tc.wantHow)
		}
	}
}

// TestTicketForBranchRefusesAnIssueNumberTwoTicketsHave: tickets mirrored to
// same-numbered issues of different repositories do not both own the branch.
func TestTicketForBranchRefusesAnIssueNumberTwoTicketsHave(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-1", "JIG-2"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	mirrorTo(t, st, "JIG-1", "acme/one", 5)
	mirrorTo(t, st, "JIG-2", "acme/two", 5)
	id, how, err := st.TicketForBranch("5-shared")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_REMOTE_ID_COLLISION" {
		t.Fatalf("TicketForBranch = %q, %q, %v; want TICKET_REMOTE_ID_COLLISION", id, how, err)
	}
	// The ticket's own id still binds the branch.
	if id, how, err := st.TicketForBranch("JIG-2-shared"); err != nil || id != "JIG-2" || how != "id" {
		t.Errorf("TicketForBranch(JIG-2-shared) = %q, %q, %v; want JIG-2, id", id, how, err)
	}
}

// TestTicketForBranchBindsAnOldJigBranchByItsAlias: after a migration renamed a
// ticket, the jig/<old id> branch begun before it binds to the ticket by the
// alias the migration left, and the branch the new naming gives binds by the id.
func TestTicketForBranchBindsAnOldJigBranchByItsAlias(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-4", Ticket{Title: "Upload item gallery", Aliases: []string{"T-12"}}); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"jig/T-12", "jig/T-12-follow-up", "T-12-upload-item-gallery"} {
		if id, how, err := st.TicketForBranch(branch); err != nil || id != "STORE-4" || how != "alias" {
			t.Errorf("TicketForBranch(%q) = %q, %q, %v; want STORE-4, alias", branch, id, how, err)
		}
	}
	if id, how, err := st.TicketForBranch("STORE-4-upload-item-gallery"); err != nil || id != "STORE-4" || how != "id" {
		t.Errorf("TicketForBranch(STORE-4-upload-item-gallery) = %q, %q, %v; want STORE-4, id", id, how, err)
	}
}

// TestBranchLeadIssue: the lead alone, without asking the store.
func TestBranchLeadIssue(t *testing.T) {
	for branch, want := range map[string]string{
		"102":                         "102",
		"102-upload-item-gallery":     "102",
		"jig/102-upload-item-gallery": "102",
		"7-x":                         "7",
		"102x":                        "",
		"0102-x":                      "",
		"0-x":                         "",
		"JIG-1":                       "",
		"feature/102-upload":          "",
		"jig/jig/102-twice":           "",
		"":                            "",
		"-102":                        "",
		"1 02":                        "",
		"102_underscore":              "",
	} {
		if got := BranchLeadIssue(branch); got != want {
			t.Errorf("BranchLeadIssue(%q) = %q, want %q", branch, got, want)
		}
	}
}

// TestTrackerIDs: the record the GitHub mirror keeps under tracker/ gives a
// ticket's issue; no record, or one with no issue, gives none; an undecodable
// one is refused naming itself.
func TestTrackerIDs(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if got, err := st.TrackerIDs("JIG-1"); err != nil || got.GitHub != nil {
		t.Errorf("TrackerIDs with no record = %+v, %v; want none", got, err)
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 102)
	got, err := st.TrackerIDs("JIG-1")
	if err != nil || got.GitHub == nil || got.GitHub.Repo != "acme/tracking" || got.GitHub.Number != 102 {
		t.Errorf("TrackerIDs = %+v, %v; want acme/tracking#102", got, err)
	}

	writeTrackerRecord(t, st, "JIG-2", "repo: acme/tracking\nitem: PVTI_x\n")
	if got, err := st.TrackerIDs("JIG-2"); err != nil || got.GitHub != nil {
		t.Errorf("TrackerIDs of a record with no issue = %+v, %v; want none", got, err)
	}
	// Keys the mirror owns that the store does not read are no matter.
	writeTrackerRecord(t, st, "JIG-3", "repo: acme/tracking\nissue: 9\nnode_id: I_9\nprs:\n  - repo: acme/tracking\n    number: 3\nsynced:\n  title: x\n")
	if got, err := st.TrackerIDs("JIG-3"); err != nil || got.GitHub == nil || got.GitHub.Number != 9 {
		t.Errorf("TrackerIDs of a full record = %+v, %v; want issue 9", got, err)
	}
	writeTrackerRecord(t, st, "JIG-4", "repo: [unclosed\n")
	_, err = st.TrackerIDs("JIG-4")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_RECORD_INVALID" {
		t.Errorf("TrackerIDs of a broken record: %v, want TRACKER_RECORD_INVALID", err)
	}
}

// TestBrokenTrackerRecordCostsOnlyItsOwnTicket: a ticket whose tracker record
// cannot be decoded cannot say its branch, but binding some other ticket's
// issue-numbered branch does not fail on it.
func TestBrokenTrackerRecordCostsOnlyItsOwnTicket(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-1", "JIG-2"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: "Upload item gallery"}); err != nil {
			t.Fatal(err)
		}
	}
	mirrorTo(t, st, "JIG-1", "acme/tracking", 102)
	writeTrackerRecord(t, st, "JIG-2", "repo: [unclosed\n")
	if id, how, err := st.TicketForBranch("102-upload-item-gallery"); err != nil || id != "JIG-1" || how != "remote" {
		t.Errorf("TicketForBranch = %q, %q, %v; want JIG-1, remote", id, how, err)
	}
	if _, err := st.TicketBranch("JIG-2", "main"); err == nil {
		t.Error("TicketBranch of the ticket with the broken record: want an error naming it")
	}
	if got, err := st.TicketBranch("JIG-1", "main"); err != nil || got != "102-upload-item-gallery" {
		t.Errorf("TicketBranch(JIG-1) = %q, %v; want 102-upload-item-gallery", got, err)
	}
}
