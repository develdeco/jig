package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
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
	got := reviewNotesWords(verifydeliver.ReviewNotesReport{Status: verifydeliver.ReviewNotesNotPosted, Reason: "the review notes are not rendered"})
	if !strings.Contains(got, "jig publish <ticket> --body") || strings.Contains(got, "by hand") && strings.Contains(got, ".md") {
		t.Errorf("row = %q, want the render command and no file to post", got)
	}
}
