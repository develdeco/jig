package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestRecordedBlocksSayHowThePullRequestReadsBack: `jig publish --pr` prints
// the body_match row of what it read back, and for a pull request that is not
// the render the help to put it right, ahead of the rest.
func TestRecordedBlocksSayHowThePullRequestReadsBack(t *testing.T) {
	t.Parallel()

	base := verifydeliver.RecordReport{Repo: "api", URL: "https://example.test/acme/api/pull/7", Branch: "jig/JIG-1", Commit: "abc", Verified: true}
	render := func(r verifydeliver.Readback) string {
		report := base
		report.Readback = r
		return strings.Join(recordedBlocks("JIG-1", report, nil), "\n")
	}

	if out := render(verifydeliver.Readback{}); strings.Contains(out, "body_match") {
		t.Errorf("a record that read nothing back prints a body_match row:\n%s", out)
	}
	for _, match := range []string{journal.BodyMatches, journal.BodyUnknown} {
		out := render(verifydeliver.Readback{Match: match})
		if !strings.Contains(out, "body_match: "+match+"\n") || strings.Contains(out, "does not carry the render") {
			t.Errorf("readback %s printed:\n%s", match, out)
		}
	}
	out := render(verifydeliver.Readback{Match: journal.BodyDiffers, Differs: "title and body"})
	if !strings.Contains(out, "body_match: differs (the title and body)") || !strings.Contains(out, "The pull request does not carry the render") || !strings.Contains(out, "jig publish JIG-1 --body") {
		t.Errorf("readback differs printed:\n%s", out)
	}
}

// TestStatusPublishShowsAPullRequestThatIsNotTheRender: a pr line that read
// back a difference shows it in the publish block, and one that matched, could
// not tell or predates the field says nothing; with several repos the repo is
// named.
func TestStatusPublishShowsAPullRequestThatIsNotTheRender(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	branch := "jig/" + fx.Ticket
	head := sha40("a")
	pr := func(match string) journal.Line {
		return journal.Line{Event: journal.EventPr, Commit: head, Branch: branch, Outcome: "recorded", URL: "https://example.test/acme/demo/pull/7", BodyMatch: match}
	}
	for match, want := range map[string]bool{"": false, journal.BodyMatches: false, journal.BodyUnknown: false, journal.BodyDiffers: true} {
		if err := journal.Append(st, fx.Ticket, pr(match)); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(block(statusOf(t, fx, st), "publish:"), "\n")
		if has := strings.Contains(got, `pr_render: "differs`); has != want {
			t.Errorf("after a pr line with body_match %q the publish block is\n%s\nwant pr_render: %v", match, got, want)
		}
	}

	// With two repos the repo that differs is named.
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Commit: head, Branch: branch, Repo: "api", Outcome: "recorded", URL: "https://example.test/acme/api/pull/7", BodyMatch: journal.BodyDiffers},
		{Event: journal.EventPr, Commit: head, Branch: branch, Repo: "web", Outcome: "recorded", URL: "https://example.test/acme/web/pull/3", BodyMatch: journal.BodyMatches},
	} {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(block(statusOf(t, fx, st), "publish:"), "\n")
	if !strings.Contains(got, `pr_render_api: "differs`) || strings.Contains(got, "pr_render_web") {
		t.Errorf("the publish block of two repos is\n%s\nwant pr_render_api only", got)
	}
}
