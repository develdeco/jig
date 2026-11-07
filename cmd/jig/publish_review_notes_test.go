package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPublishSaysWhatBecameOfTheReviewNotes: a bare publish in a repo with no
// pull-request host has nowhere to post the review notes, and says so in a
// review_notes row with the reason and the file to post by hand.
func TestPublishSaysWhatBecameOfTheReviewNotes(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--yes", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish: exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"review_notes[1]{repo,outcome}:", "not posted: no pull-request host", "review-notes.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("publish output lacks %q:\n%s", want, out)
		}
	}
}

// TestRecordedBlocksSayWhatBecameOfTheReviewNotes: `jig publish --pr` prints a
// review_notes row as posted, already posted, or not posted with the reason and
// the file to post by hand, and no row when the report says nothing.
func TestRecordedBlocksSayWhatBecameOfTheReviewNotes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		notes verifydeliver.ReviewNotesReport
		want  string
	}{
		{"posted", verifydeliver.ReviewNotesReport{Status: verifydeliver.ReviewNotesPosted}, "  review_notes: posted"},
		{"already posted", verifydeliver.ReviewNotesReport{Status: verifydeliver.ReviewNotesAlreadyPosted}, "  review_notes: already posted"},
		{"not posted", verifydeliver.ReviewNotesReport{Status: verifydeliver.ReviewNotesNotPosted, Reason: "gh: boom", File: "/store/pr/review-notes.md"}, "not posted: gh: boom; post /store/pr/review-notes.md as a comment"},
		{"unsaid", verifydeliver.ReviewNotesReport{}, ""},
	} {
		got := recordedBlocks("JIG-7", verifydeliver.RecordReport{Repo: "api", URL: "u", Verified: true, ReviewNotes: tc.notes}, nil)[0]
		if tc.want == "" {
			if strings.Contains(got, "review_notes") {
				t.Errorf("%s: block has a review_notes row:\n%s", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: block lacks %q:\n%s", tc.name, tc.want, got)
		}
	}
}

// TestUnrenderedReviewNotesNameTheCommandNotAFile: notes that were never
// rendered have no file to post by hand, so the row names the command that
// renders them.
func TestUnrenderedReviewNotesNameTheCommandNotAFile(t *testing.T) {
	t.Parallel()
	got := reviewNotesWords("JIG-7", verifydeliver.ReviewNotesReport{Status: verifydeliver.ReviewNotesNotPosted, Reason: "the review notes are not rendered"})
	if !strings.Contains(got, "jig publish JIG-7 --body") || strings.Contains(got, "by hand") && strings.Contains(got, ".md") {
		t.Errorf("row = %q, want the render command and no file to post", got)
	}
}

// TestBarePublishJournalsAPipedYesAsAnAgents: a bare publish answered by a
// scripted y reads the answer from the same stdin it asks about the terminal.
// Off a terminal the yes is journaled as an agent's publish-approved line and
// the review notes say an agent's; at a terminal it is a person's. A mutant that
// makes every yes a person's, or none, fails one of the two.
func TestBarePublishJournalsAPipedYesAsAnAgents(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		terminal   bool
		provenance string
		notes      string
	}{
		{"piped y, not a terminal", false, "agent", "Publish approved by an agent"},
		{"typed y at a terminal", true, "person", "at a terminal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			jigHome := t.TempDir()
			e := testEnv(jigHome)
			if c.terminal {
				e = e.atTerminal()
			}
			fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
			buildFixtureTicket(t, fx)
			gateCleanThroughMain(t, testEnv(jigHome), fx)

			out, code := runMain(t, e, "y\n", "publish", fx.Ticket, "--store", fx.StoreDir)
			if code != 0 {
				t.Fatalf("publish: exit = %d, want 0\n%s", code, out)
			}
			st, err := store.Open(fx.StoreDir)
			if err != nil {
				t.Fatal(err)
			}
			lines, err := journal.Read(st, fx.Ticket)
			if err != nil {
				t.Fatal(err)
			}
			approvals := 0
			for _, l := range lines {
				if l.Event == journal.EventPublishApproved {
					approvals++
					if l.Provenance != c.provenance {
						t.Errorf("publish-approved line = %+v, want provenance %s", l, c.provenance)
					}
				}
			}
			if approvals != 1 {
				t.Fatalf("%d publish-approved line(s), want 1 for the yes", approvals)
			}
			notes, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "review-notes.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(notes), c.notes) {
				t.Errorf("review notes lack %q:\n%s", c.notes, notes)
			}
		})
	}
}
