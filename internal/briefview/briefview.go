// Package briefview shows a ticket's brief to a person: its "## " sections as
// plain HTML, a link to the illustration beside it, and the illustration
// itself, served on the loopback address. It is jig's one viewer, and a
// minimal one: no script, no stylesheet or font fetched, no other file of the
// store reachable. Any other viewer is the project's choice and never named
// here (chart decision 14).
package briefview

import (
	"bytes"
	"errors"
	"html"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/store"
)

// IllustrationRoute is where the viewer serves the illustration.
const IllustrationRoute = "/" + store.IllustrationFile

// Source names what the viewer shows. The files are read again on every
// request, so an edit of either shows on the next reload.
type Source struct {
	// Ticket is the ticket's id, and Title its title (empty when it has none).
	Ticket string
	Title  string
	// BriefPath is the ticket's brief.md and IllustrationPath its
	// illustration.html, which may not exist.
	BriefPath        string
	IllustrationPath string
}

// Handler serves src: the page at "/" and the illustration at IllustrationRoute.
// It answers only requests addressed to the loopback host, so a page in a
// browser cannot read the brief through a name that resolves there (DNS
// rebinding), and nothing else of the store is reachable.
func Handler(src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		brief, err := os.ReadFile(src.BriefPath)
		if err != nil {
			http.Error(w, "this ticket has no brief.md", http.StatusNotFound)
			return
		}
		_, err = os.Stat(src.IllustrationPath)
		write(w, "text/html; charset=utf-8", Page(src.Ticket, src.Title, brief, err == nil))
	})
	mux.HandleFunc("GET "+IllustrationRoute, func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(src.IllustrationPath)
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "this brief has no illustration", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "the illustration cannot be read", http.StatusInternalServerError)
			return
		}
		write(w, "text/html; charset=utf-8", data)
	})
	return loopbackOnly(mux)
}

// write sends body as contentType, uncached: the file may change under it.
func write(w http.ResponseWriter, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(body)
}

// loopbackOnly refuses a request whose Host is not the loopback address.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		switch host {
		case "127.0.0.1", "localhost", "::1", "[::1]":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "this viewer answers on the loopback address only", http.StatusForbidden)
		}
	})
}

// Page renders the viewer's page for a ticket: its id and title, a contents
// list and each "## " section of briefMD, and a link to the illustration when
// there is one. The text before the first section is shown above them.
func Page(ticket, title string, briefMD []byte, hasIllustration bool) []byte {
	text := strings.ReplaceAll(string(briefMD), "\r\n", "\n")
	sections := store.BriefSections(briefMD)
	preamble := text
	if i := firstSectionOffset(text); i >= 0 {
		preamble = text[:i]
	}

	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(strings.TrimSpace(ticket+" "+Title(briefMD))) + "</title>\n")
	b.WriteString("<style>" + pageCSS + "</style>\n</head>\n<body>\n<main>\n")
	b.WriteString("<header>\n<p class=\"ticket\">" + html.EscapeString(ticket) + "</p>\n")
	if title != "" {
		b.WriteString("<p class=\"title\">" + html.EscapeString(title) + "</p>\n")
	}
	b.WriteString("</header>\n")
	if hasIllustration {
		b.WriteString("<p class=\"illustration\"><a href=\"" + IllustrationRoute + "\">Illustration</a></p>\n")
	}
	if strings.TrimSpace(preamble) != "" {
		b.WriteString(markdownHTML(preamble))
	}
	if len(sections) > 1 {
		b.WriteString("<nav aria-label=\"Sections\">\n<ol>\n")
		for i, sec := range sections {
			b.WriteString("<li><a href=\"#s" + strconv.Itoa(i+1) + "\">" + html.EscapeString(sec.Heading) + "</a></li>\n")
		}
		b.WriteString("</ol>\n</nav>\n")
	}
	for i, sec := range sections {
		b.WriteString("<section id=\"s" + strconv.Itoa(i+1) + "\">\n<h2>" + inline(sec.Heading) + "</h2>\n")
		b.WriteString(markdownHTML(sec.Body))
		b.WriteString("</section>\n")
	}
	b.WriteString("</main>\n</body>\n</html>\n")
	return b.Bytes()
}

// firstSectionOffset is the offset of the first "## " heading line of text,
// or -1 when it has none: where store.BriefSections starts counting.
func firstSectionOffset(text string) int {
	if strings.HasPrefix(text, "## ") {
		return 0
	}
	if i := strings.Index(text, "\n## "); i >= 0 {
		return i + 1
	}
	return -1
}

// Title is the brief's own title: the text of the first "# " heading before
// the first section, or "" when it has none.
func Title(briefMD []byte) string {
	text := strings.ReplaceAll(string(briefMD), "\r\n", "\n")
	if i := firstSectionOffset(text); i >= 0 {
		text = text[:i]
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// summaryLines and summaryRunes bound Summary.
const (
	summaryLines = 2
	summaryRunes = 160
)

// Summary is the first line or two of a section's body: its first two lines
// that are not blank, trimmed and joined by a space, cut at summaryRunes with
// "..." when longer.
func Summary(body string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
			if len(lines) == summaryLines {
				break
			}
		}
	}
	s := strings.Join(lines, " ")
	if utf8.RuneCountInString(s) <= summaryRunes {
		return s
	}
	return string([]rune(s)[:summaryRunes]) + "..."
}

// pageCSS is the page's whole stylesheet: system fonts, a reading width, and
// colors that follow the viewer's light or dark preference.
const pageCSS = `
:root{color-scheme:light dark;--bg:#fbfbfa;--fg:#1d1d1b;--muted:#5f5f5a;--rule:#d9d9d3;--code:#efeee9;--link:#0b5cad}
@media (prefers-color-scheme:dark){:root{--bg:#161615;--fg:#e8e8e4;--muted:#a3a39b;--rule:#33332f;--code:#242422;--link:#7db7f0}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:46rem;margin:0 auto;padding:2rem 16px 4rem}
.ticket{margin:0;color:var(--muted);font:600 .85rem/1.2 ui-monospace,Consolas,monospace}
.title{margin:.2rem 0 1.2rem;font-size:1.5rem;font-weight:650;line-height:1.25}
h1{font-size:1.6rem;line-height:1.25}
h2{margin-top:2.2rem;padding-top:1rem;border-top:1px solid var(--rule);font-size:1.25rem}
h3,h4,h5,h6{margin-top:1.6rem;font-size:1.05rem}
a{color:var(--link)}
nav ol{padding-left:1.4rem;color:var(--muted)}
code,pre{background:var(--code);font:.9em ui-monospace,Consolas,monospace}
code{padding:.1em .3em;border-radius:3px}
pre{padding:.8rem 1rem;overflow-x:auto;border-radius:4px}
pre code{padding:0;background:none}
blockquote{margin:1rem 0;padding-left:1rem;border-left:3px solid var(--rule);color:var(--muted)}
.illustration{margin:0 0 1.5rem}
`
