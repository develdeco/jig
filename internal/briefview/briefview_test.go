package briefview

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleBrief = "# Sample brief\n\nA line before the sections.\n\n## Goal\n\nShow the thing, with `code` and **weight**.\n\n## Steps\n\n- first\n- second\n  - nested\n- third\n\n1. one\n2. two\n\n```\n<script>alert(1)</script>\n```\n"

// get fetches path from h as a request addressed to host.
func get(h http.Handler, host, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rig writes a brief (and an illustration when illustration is not empty)
// into a directory and returns the handler for them.
func rig(t *testing.T, brief, illustration string) (http.Handler, Source) {
	t.Helper()
	dir := t.TempDir()
	src := Source{
		Ticket: "JIG-1", Title: "Show it",
		BriefPath:        filepath.Join(dir, "brief.md"),
		IllustrationPath: filepath.Join(dir, "illustration.html"),
	}
	if brief != "" {
		if err := os.WriteFile(src.BriefPath, []byte(brief), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if illustration != "" {
		if err := os.WriteFile(src.IllustrationPath, []byte(illustration), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Handler(src), src
}

// TestPageShowsTheSections: the page names the ticket, lists the sections,
// renders each one's body, links the illustration when there is one, and
// escapes what the brief holds.
func TestPageShowsTheSections(t *testing.T) {
	t.Parallel()
	page := string(Page("JIG-1", "Show it", []byte(sampleBrief), true))
	for _, want := range []string{
		"<title>JIG-1 Sample brief</title>",
		`<p class="ticket">JIG-1</p>`,
		`<p class="title">Show it</p>`,
		"<h1>Sample brief</h1>",
		"<p>A line before the sections.</p>",
		`<a href="/illustration.html">Illustration</a>`,
		`<a href="#s1">Goal</a>`, `<a href="#s2">Steps</a>`,
		`<section id="s1">`, "<h2>Goal</h2>",
		"<p>Show the thing, with <code>code</code> and <strong>weight</strong>.</p>",
		"<h2>Steps</h2>",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<script") {
		t.Errorf("the page holds a script element:\n%s", page)
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Errorf("the page names an address it could fetch from:\n%s", page)
	}
	if without := string(Page("JIG-1", "", []byte(sampleBrief), false)); strings.Contains(without, "Illustration") {
		t.Errorf("a brief with no illustration links one:\n%s", without)
	}
}

// TestMarkdownLists: lists nest by indent and close in order, and an
// indented line goes on the item above it.
func TestMarkdownLists(t *testing.T) {
	t.Parallel()
	got := markdownHTML("- a\n- b\n  - c\n  - d\n- e\n\n1. x\n   more\n2. y\n")
	want := "<ul>\n<li>a</li>\n<li>b<ul>\n<li>c</li>\n<li>d</li>\n</ul>\n</li>\n<li>e</li>\n</ul>\n" +
		"<ol>\n<li>x more</li>\n<li>y</li>\n</ol>\n"
	if got != want {
		t.Errorf("the lists rendered as\n%s\nwant\n%s", got, want)
	}
}

// TestMarkdownBlocks: headings, quotes, fenced code, tables and links.
func TestMarkdownBlocks(t *testing.T) {
	t.Parallel()
	got := markdownHTML("### Part\n\n> quoted\n> text\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nSee [the docs](https://example.invalid/a?x=1&y=2) and [bad](javascript:alert(1)).\n")
	for _, want := range []string{
		"<h3>Part</h3>",
		"<blockquote>\n<p>quoted text</p>\n</blockquote>",
		"<pre>| a | b |\n|---|---|\n| 1 | 2 |</pre>",
		`<a href="https://example.invalid/a?x=1&amp;y=2" rel="noopener noreferrer">the docs</a>`,
		"[bad](javascript:alert(1))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `href="javascript`) {
		t.Errorf("a javascript: link was rendered as a link:\n%s", got)
	}
}

// TestHandlerServesPageAndIllustration: the page and the illustration are
// served as they are on disk now, and anything else is not found.
func TestHandlerServesPageAndIllustration(t *testing.T) {
	t.Parallel()
	h, src := rig(t, sampleBrief, "<h1>The picture</h1>")

	page := get(h, "127.0.0.1:4719", "GET", "/")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "<h2>Goal</h2>") || !strings.Contains(page.Body.String(), `href="/illustration.html"`) {
		t.Fatalf("GET /: %d\n%s", page.Code, page.Body)
	}
	if ct := page.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("the page's content type is %q", ct)
	}
	ill := get(h, "127.0.0.1:4719", "GET", "/illustration.html")
	if ill.Code != 200 || ill.Body.String() != "<h1>The picture</h1>" {
		t.Fatalf("GET /illustration.html: %d %q", ill.Code, ill.Body)
	}
	if cc := ill.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("the illustration's Cache-Control is %q, want no-store: it may change under the viewer", cc)
	}

	// An edit shows on the next request.
	if err := os.WriteFile(src.IllustrationPath, []byte("<h1>Redrawn</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ill := get(h, "127.0.0.1:4719", "GET", "/illustration.html"); ill.Body.String() != "<h1>Redrawn</h1>" {
		t.Errorf("after an edit the illustration reads %q", ill.Body)
	}

	// Nothing else of the folder is reachable (the router may redirect a path it cleans).
	for _, path := range []string{"/brief.md", "/approval.yaml", "/../ticket.yaml", "/illustration.html/x"} {
		if rec := get(h, "127.0.0.1:4719", "GET", path); rec.Code == 200 {
			t.Errorf("GET %s was served (%d)", path, rec.Code)
		}
	}
	if rec := get(h, "127.0.0.1:4719", "POST", "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d, want 405", rec.Code)
	}
}

// TestHandlerWithoutAnIllustration: no illustration is a 404 and no link.
func TestHandlerWithoutAnIllustration(t *testing.T) {
	t.Parallel()
	h, _ := rig(t, sampleBrief, "")
	if rec := get(h, "localhost:4719", "GET", "/illustration.html"); rec.Code != 404 {
		t.Errorf("GET /illustration.html with none: %d, want 404", rec.Code)
	}
	if page := get(h, "localhost:4719", "GET", "/"); page.Code != 200 || strings.Contains(page.Body.String(), "illustration.html") {
		t.Errorf("GET / with no illustration: %d\n%s", page.Code, page.Body)
	}
}

// TestHandlerWithoutABrief: a ticket with no brief.md is not found.
func TestHandlerWithoutABrief(t *testing.T) {
	t.Parallel()
	h, _ := rig(t, "", "<p>x</p>")
	if rec := get(h, "127.0.0.1:4719", "GET", "/"); rec.Code != 404 {
		t.Errorf("GET / with no brief: %d, want 404", rec.Code)
	}
}

// TestHandlerAnswersTheLoopbackOnly: a request addressed to another host name
// is refused, whatever the path.
func TestHandlerAnswersTheLoopbackOnly(t *testing.T) {
	t.Parallel()
	h, _ := rig(t, sampleBrief, "<p>x</p>")
	for _, host := range []string{"127.0.0.1:4719", "127.0.0.1", "localhost:80", "[::1]:4719"} {
		if rec := get(h, host, "GET", "/"); rec.Code != 200 {
			t.Errorf("Host %s: %d, want 200", host, rec.Code)
		}
	}
	for _, host := range []string{"evil.example:4719", "192.168.0.7:4719", "127.0.0.1.evil.example"} {
		for _, path := range []string{"/", "/illustration.html"} {
			rec := get(h, host, "GET", path)
			if body, _ := io.ReadAll(rec.Body); rec.Code != http.StatusForbidden || strings.Contains(string(body), "Goal") {
				t.Errorf("Host %s GET %s: %d %q, want 403 and no brief", host, path, rec.Code, body)
			}
		}
	}
}

// TestTitleAndSummary: the title is the first "# " heading before the
// sections; a summary is the first two non-blank lines, cut when long.
func TestTitleAndSummary(t *testing.T) {
	t.Parallel()
	if got := Title([]byte(sampleBrief)); got != "Sample brief" {
		t.Errorf("Title = %q", got)
	}
	if got := Title([]byte("## Goal\n\n# not a title\n")); got != "" {
		t.Errorf("Title of a brief that opens with a section = %q, want none", got)
	}
	if got := Summary("\n\n  First line.  \n\nSecond line.\nThird line.\n"); got != "First line. Second line." {
		t.Errorf("Summary = %q", got)
	}
	long := Summary(strings.Repeat("word ", 100))
	if !strings.HasSuffix(long, "...") || len([]rune(long)) != summaryRunes+3 {
		t.Errorf("a long summary is %d runes: %q", len([]rune(long)), long)
	}
	if got := Summary(""); got != "" {
		t.Errorf("Summary of nothing = %q", got)
	}
}
