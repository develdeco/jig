package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// sinceRig is a fixture ticket whose brief.md the test rewrites, with the
// status as it renders now.
type sinceRig struct {
	t      *testing.T
	st     *store.Store
	ticket string
	e      env
}

func newSinceRig(t *testing.T, brief string) *sinceRig {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	r := &sinceRig{t: t, st: st, ticket: fx.Ticket, e: testEnv(fx.Home).withClock(fixedClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))}
	r.write("brief.md", brief)
	return r
}

func (r *sinceRig) write(name, text string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.st.TicketDir(r.ticket), name), []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *sinceRig) approve(provenance string) store.Approval {
	r.t.Helper()
	sum, err := r.st.BriefSHA256(r.ticket)
	if err != nil {
		r.t.Fatal(err)
	}
	a, _, err := recordApproval(r.e, r.st, r.ticket, sum, provenance)
	if err != nil {
		r.t.Fatal(err)
	}
	return a
}

func (r *sinceRig) status() string {
	r.t.Helper()
	out, err := RenderStatus(r.st, r.ticket)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *sinceRig) want(out string, parts ...string) {
	r.t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			r.t.Errorf("status lacks %q:\n%s", p, out)
		}
	}
}

func (r *sinceRig) wantNot(out string, parts ...string) {
	r.t.Helper()
	for _, p := range parts {
		if strings.Contains(out, p) {
			r.t.Errorf("status holds %q:\n%s", p, out)
		}
	}
}

const sinceBrief = "# T\n\n## Alpha\n\nfirst\n\n## Beta\n\nsecond\n\n## Gamma\n\nthird\n"

// TestRecordApprovalRecordsTheSectionsItApproved: the approval it writes holds
// each section's heading and hash and the illustration's hash, and an approval
// left as it is (same hash, a word as strong) keeps what it holds, even a
// record that has no sections.
func TestRecordApprovalRecordsTheSectionsItApproved(t *testing.T) {
	t.Parallel()
	r := newSinceRig(t, sinceBrief)
	r.write(store.IllustrationFile, "<p>one</p>")
	a := r.approve("person")
	var want []store.ApprovedSection
	for _, sec := range store.BriefSections([]byte(sinceBrief)) {
		want = append(want, store.ApprovedSection{Heading: sec.Heading, Hash: sec.Hash})
	}
	if !reflect.DeepEqual(a.Sections, want) || a.IllustrationSHA256 == "" {
		t.Fatalf("the approval holds %+v and illustration %q, want %+v and a hash", a.Sections, a.IllustrationSHA256, want)
	}
	if got, ok, err := r.st.ReadApproval(r.ticket); err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Errorf("the approval on disk = %+v, %v, %v, want %+v", got, ok, err, a)
	}

	// An approval made before the record, approved again as the same brief by
	// a word no stronger, stays as it is: it never gains a record it did not
	// make.
	sum, err := r.st.BriefSHA256(r.ticket)
	if err != nil {
		t.Fatal(err)
	}
	old := store.Approval{By: a.By, Email: a.Email, At: a.At, BriefSHA256: sum, Provenance: "person"}
	if err := r.st.WriteApproval(r.ticket, old); err != nil {
		t.Fatal(err)
	}
	if _, result, err := recordApproval(r.e, r.st, r.ticket, sum, "agent"); err != nil || result != approvalUnchanged {
		t.Fatalf("approving again = %q, %v, want unchanged", result, err)
	}
	if got, _, _ := r.st.ReadApproval(r.ticket); len(got.Sections) != 0 || got.IllustrationSHA256 != "" {
		t.Errorf("an approval left as it is gained a record: %+v", got)
	}
}

// TestStatusBriefSaysWhichSectionsChangedSinceTheApproval: each section reads
// same, or changed when edited or new, the headings the brief lost are listed,
// the illustration reads none, same, changed, added or removed, and an
// approval that recorded no sections, or none at all, says nothing of change.
func TestStatusBriefSaysWhichSectionsChangedSinceTheApproval(t *testing.T) {
	t.Parallel()

	r := newSinceRig(t, sinceBrief)
	out := r.status()
	r.want(out, "brief_sections[3]{heading,slices,since_approval}:\n  Alpha,-,-\n  Beta,-,-\n  Gamma,-,-\n")
	r.wantNot(out, "illustration_since_approval", "brief_removed")

	r.approve("person")
	out = r.status()
	r.want(out, "  Alpha,-,same\n  Beta,-,same\n  Gamma,-,same\n", "  illustration_since_approval: none\n")
	r.wantNot(out, "brief_removed")

	// An edited section, a new one, a removed one, and the illustration added.
	r.write("brief.md", "# T\n\n## Alpha\n\nfirst\n\n## Beta\n\nsecond, edited\n\n## Delta\n\nnew\n")
	r.write(store.IllustrationFile, "<p>one</p>")
	out = r.status()
	r.want(out, "  Alpha,-,same\n  Beta,-,changed\n  Delta,-,changed\n", "brief_removed[1]{heading}:\n  Gamma\n", "  illustration_since_approval: added\n")

	// Approved with an illustration: unchanged, changed, then removed.
	r.approve("person")
	r.want(r.status(), "  illustration_since_approval: same\n")
	r.write(store.IllustrationFile, "<p>two</p>")
	r.want(r.status(), "  illustration_since_approval: changed\n")
	if err := os.Remove(filepath.Join(r.st.TicketDir(r.ticket), store.IllustrationFile)); err != nil {
		t.Fatal(err)
	}
	r.want(r.status(), "  illustration_since_approval: removed\n")

	// An approval that recorded no sections says nothing of what changed.
	sum, err := r.st.BriefSHA256(r.ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.WriteApproval(r.ticket, store.Approval{By: "Pat Example", At: "2026-10-01T00:00:00Z", BriefSHA256: sum, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	out = r.status()
	r.want(out, "  Alpha,-,-\n  Beta,-,-\n  Delta,-,-\n", "  illustration_since_approval: -\n")
	r.wantNot(out, "brief_removed")
}
