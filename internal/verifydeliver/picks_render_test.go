package verifydeliver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
)

// This file pins, one rule at a time, what renderPicksSection does to the
// words of a pick (its summary, flow titles and captions) before they reach a
// published, public pull request body. The pick session's words are jig's
// input, not jig's text: the section leaves host paths out whole, keeps a
// caption inside its own bullet and image reference, and keeps HTML out.

// renderPickWords renders a pick of one flow holding the one candidate id
// ("r1" is an image, "r3" a video), staged as rec-1.<ext>, with the given
// summary and caption, and returns the result and the staged file's name.
func renderPickWords(t *testing.T, id, summary, caption string, dirs ...hostDir) (DemoRenderResult, string) {
	t.Helper()
	cands := pickTestCands()
	p, err := resolvePicks(PicksResult{
		Summary: summary,
		Flows:   []PicksFlow{{Title: "Flow", Items: []PicksItem{{ID: id, Caption: caption}}}},
	}, cands)
	if err != nil {
		t.Fatalf("resolvePicks: %v", err)
	}
	name := "rec-1." + p.items()[0].cand.ext()
	return renderPicksSection(p, "/stage", []DemoFile{{Name: name}}, dirs), name
}

// TestRenderPicksSectionLeavesOutASummaryNamingTheJigHome: the owner's
// decision on r1-f13 (DECISIONS.md) - a summary is the session's own words,
// and a published pull request body is public, so one that repeats the jig
// home back is left out of the rendered section whole, not merely edited, and
// reported in ScrubbedSummary. The file beside it, whose own caption names no
// such path, still renders.
func TestRenderPicksSectionLeavesOutASummaryNamingTheJigHome(t *testing.T) {
	t.Parallel()
	jigHome := filepath.Join(t.TempDir(), "jig-home")
	summary := "wrote the screenshot under " + jigHome + " and it worked"
	res, name := renderPickWords(t, "r1", summary, "fine caption", hostDir{jigHome, "<jig home>"})
	if !res.ScrubbedSummary {
		t.Error("ScrubbedSummary = false, want true")
	}
	if strings.Contains(res.Section, jigHome) || strings.Contains(res.Section, "worked") {
		t.Errorf("Section = %q, want the whole summary left out, not just the path inside it", res.Section)
	}
	if !strings.Contains(res.Section, "- ![fine caption](./"+name+")") {
		t.Errorf("Section = %q, want the file's own caption still rendered", res.Section)
	}
}

// TestRenderPicksSectionLeavesOutACaptionNamingTheStagingDirectory: the same
// rule for a caption, the per-file half of the session's own words: one
// naming the staging directory is left out and the file named in
// ScrubbedCaptions, but the file reference itself (which names no host path)
// still renders, with no caption.
func TestRenderPicksSectionLeavesOutACaptionNamingTheStagingDirectory(t *testing.T) {
	t.Parallel()
	staged := filepath.Join(t.TempDir(), "evidence", "id", "JIG-1", "picks", "abc123")
	res, name := renderPickWords(t, "r1", "it works", "see "+staged+" for the raw frame", hostDir{staged, "<picks dir>"})
	if fmt.Sprint(res.ScrubbedCaptions) != "["+name+"]" {
		t.Errorf("ScrubbedCaptions = %v, want [%s]", res.ScrubbedCaptions, name)
	}
	if strings.Contains(res.Section, staged) || strings.Contains(res.Section, "raw frame") {
		t.Errorf("Section = %q, still names the staging directory or carries its caption", res.Section)
	}
	if !strings.Contains(res.Section, "- ![](./"+name+")\n") {
		t.Errorf("Section = %q, want the file still listed with no caption", res.Section)
	}
}

// TestRenderPicksSectionLeavesOutACaptionNamingTheGateLeaseAsAWSLMount: the
// gate lease is one of the directories a session may have been told about,
// and on Windows herdr hands it over spelled as a WSL mount
// (session.respellMentions), not jig's own spelling - so that spelling, not
// only the raw one, must be checked for too.
func TestRenderPicksSectionLeavesOutACaptionNamingTheGateLeaseAsAWSLMount(t *testing.T) {
	t.Parallel()
	leaseDir, err := pool.Dir(t.TempDir(), "repo", "JIG-1", pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	res, name := renderPickWords(t, "r1", "it works", "ran it from "+session.WSLPath(leaseDir), hostDir{leaseDir, "<lease>"})
	if fmt.Sprint(res.ScrubbedCaptions) != "["+name+"]" {
		t.Errorf("ScrubbedCaptions = %v, want [%s] (the gate lease's WSL mount spelling)", res.ScrubbedCaptions, name)
	}
}

// TestRenderPicksSectionCapsAnOverlongCaption: a caption that names no host
// path is still a short label, not a paragraph, in a published pull request
// body, so it is truncated at demoCaptionRenderCap runes with a trailing
// "...". Capping is not scrubbing: the file is not reported in
// ScrubbedCaptions.
func TestRenderPicksSectionCapsAnOverlongCaption(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", demoCaptionRenderCap+50)
	res, name := renderPickWords(t, "r1", "it works", long)
	want := "- ![" + strings.Repeat("a", demoCaptionRenderCap) + "...](./" + name + ")\n"
	if !strings.Contains(res.Section, want) {
		t.Errorf("Section = %q, want the caption capped at %d runes with \"...\"", res.Section, demoCaptionRenderCap)
	}
	if len(res.ScrubbedCaptions) != 0 {
		t.Errorf("ScrubbedCaptions = %v, want none: capping is not scrubbing", res.ScrubbedCaptions)
	}
}

// TestRenderPicksSectionNeverScrubsAPathJigDidNotHandOut: the owner's decision
// is to compare known strings only, never a pattern. A caption that merely
// looks like an absolute path, but names none of jig's own directories, is
// rendered exactly as the session wrote it.
func TestRenderPicksSectionNeverScrubsAPathJigDidNotHandOut(t *testing.T) {
	t.Parallel()
	jigHome := filepath.Join(t.TempDir(), "jig-home")
	caption := "see " + filepath.Join(t.TempDir(), "unrelated", "elsewhere.png")
	res, _ := renderPickWords(t, "r1", "it works", caption, hostDir{jigHome, "<jig home>"})
	if len(res.ScrubbedCaptions) != 0 {
		t.Errorf("ScrubbedCaptions = %v, want none: the path named is not one of jig's own directories", res.ScrubbedCaptions)
	}
	if !strings.Contains(res.Section, caption) {
		t.Errorf("Section = %q, want the unrelated caption rendered as written", res.Section)
	}
}

// TestRenderPicksSectionDemotesHeadingsAndClosesAnOpenFenceInTheSummary: the
// session's own summary gets the same treatment renderIntentSection gives a
// brief or doc it did not write (demoteHeadings, closeOpenFence), so it cannot
// forge or outrank one of jig's own "## " sections or leave a fence open over
// the rest of the published body.
func TestRenderPicksSectionDemotesHeadingsAndClosesAnOpenFenceInTheSummary(t *testing.T) {
	t.Parallel()
	summary := "it works.\n\n## Verification\n\nall green\n\n```\nunclosed fence"
	res, _ := renderPickWords(t, "r1", summary, "fine caption")
	if strings.Contains(strings.TrimPrefix(res.Section, "## Demo\n"), "\n## ") {
		t.Errorf("Section = %q, want the summary's own heading demoted below section level", res.Section)
	}
	if strings.Count(res.Section, "```")%2 != 0 {
		t.Errorf("Section = %q, want the summary's open fence closed", res.Section)
	}
}

// TestRenderPicksSectionCollapsesAMultiLineCaptionToOneLine: a caption that
// clears the host-path check is still collapsed to one line (oneLine) before
// capping, so it cannot break out of its own bullet or, for an image, out of
// the alt text of its image reference.
func TestRenderPicksSectionCollapsesAMultiLineCaptionToOneLine(t *testing.T) {
	t.Parallel()
	res, name := renderPickWords(t, "r1", "it works", "the clamp holds\n\n## Verification\n\nall green")
	if want := "- ![the clamp holds ## Verification all green](./" + name + ")\n"; !strings.Contains(res.Section, want) {
		t.Errorf("Section = %q, want the caption collapsed to one line (%q)", res.Section, want)
	}
}

// TestRenderPicksSectionCaptionCannotBreakOutOfTheImageAltText: r2-f6, a
// caption carrying its own "](" closes the alt text early and reads whatever
// follows as the image's own URL, which lets the session's own words point the
// rendered image at an address of their choosing. sanitizeDemoCaption drops
// '[', ']', '(' and ')' so the caption can never supply either half of that
// construct.
func TestRenderPicksSectionCaptionCannotBreakOutOfTheImageAltText(t *testing.T) {
	t.Parallel()
	res, name := renderPickWords(t, "r1", "it works", "x](https://evil.example/pixel.png) ![y")
	if want := "- ![xhttps://evil.example/pixel.png !y](./" + name + ")\n"; !strings.Contains(res.Section, want) {
		t.Errorf("Section = %q, want %q", res.Section, want)
	}
	if strings.Contains(res.Section, "](https://evil.example") {
		t.Errorf("Section = %q, the caption's own URL must never close the image reference early", res.Section)
	}
}

// TestRenderPicksSectionCaptionWithAnUnbalancedBracketStillShowsTheImage:
// r2-f6, an unbalanced "[" in the caption would otherwise pair with the "]"
// jig itself writes to close the alt text, losing the image reference (and so
// the recording) entirely. sanitizeDemoCaption drops the bracket instead of
// letting it pair across the boundary jig did not intend.
func TestRenderPicksSectionCaptionWithAnUnbalancedBracketStillShowsTheImage(t *testing.T) {
	t.Parallel()
	res, name := renderPickWords(t, "r1", "it works", "clamp holds at [10")
	if want := "- ![clamp holds at 10](./" + name + ")\n"; !strings.Contains(res.Section, want) {
		t.Errorf("Section = %q, want %q (the image reference still rendered)", res.Section, want)
	}
}

// TestRenderPicksSectionEscapesHTMLInSummaryAndCaption: gate finding r4-f3,
// the owner's decision. Neither the summary's demoteHeadings/closeOpenFence
// nor sanitizeDemoCaption's bracket strip touches raw HTML, which GitHub
// renders a sanitized subset of inside a pull request body - a video or
// captionless bullet's caption, and the summary outright, could otherwise put
// a live img or a tag into the published body. escapeDemoHTML renders '<',
// '>' and '&' as entities in both, so such a caption or summary reaches the
// body as literal text, never a tag.
func TestRenderPicksSectionEscapesHTMLInSummaryAndCaption(t *testing.T) {
	t.Parallel()
	words := `<img src="https://evil.example/pixel.png">`
	res, name := renderPickWords(t, "r3", words, words)
	if strings.Contains(res.Section, "<img") {
		t.Errorf("Section = %q, want no raw <img> tag anywhere", res.Section)
	}
	wantSummary := "&lt;img src=\"https://evil.example/pixel.png\"&gt;\n\n"
	if !strings.Contains(res.Section, wantSummary) {
		t.Errorf("Section = %q, want the summary escaped to %q", res.Section, wantSummary)
	}
	wantCaption := "- ./" + name + ": &lt;img src=\"https://evil.example/pixel.png\"&gt;\n"
	if !strings.Contains(res.Section, wantCaption) {
		t.Errorf("Section = %q, want the caption escaped to %q", res.Section, wantCaption)
	}
}
