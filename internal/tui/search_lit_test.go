package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// litSpans are the letters a styled line draws lit, in order.
func litSpans(line string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\x1b\[1;4m(.*?)\x1b\[22;24m`).FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}

// styledLine is the line of the row id on the page main draws, styled.
func styledLine(t *testing.T, m *Model, id string) string {
	t.Helper()
	pg := m.page()
	refs := 0
	for _, l := range pg.lines {
		if l.ref != nil {
			refs++
			if l.ref.id == id {
				return l.text
			}
		}
	}
	t.Fatalf("no row %s among %d on the page", id, refs)
	return ""
}

// styledConfig is a configuration whose model draws with colours.
func styledConfig() Config {
	cfg := quietConfig()
	cfg.Renderer = colored()
	return cfg
}

// TestSearchLightsTheMatchedLetters: the letters a term matched are lit in the
// id, the title and the words of the row's detail, and in no word that starts
// no term; a word the line does not draw lights nothing and the row is still
// listed; a title the layout cut keeps the spans inside the text that stays.
func TestSearchLightsTheMatchedLetters(t *testing.T) {
	t.Parallel()
	m := searchStoreIn(t, styledConfig())
	typed(m, "/rel 11")
	line := styledLine(t, m, "REL-11")
	// The id, the title's word, and the id again in the words that say what to
	// do with it.
	if got, want := strings.Join(litSpans(line), "|"), "REL|11|rel|REL|11"; got != want {
		t.Errorf("the lit letters of /rel 11 are %q, want %q in %q", got, want, line)
	}
	if strings.Contains(stripANSI(line), "\x1b") || stripANSI(line) != ansi.Strip(line) {
		t.Errorf("the line has codes the plain text does not drop: %q", line)
	}

	// A prefix lights only the letters typed: /re lights the start of release.
	press(m, tea.KeyEsc)
	typed(m, "/rele")
	if got := strings.Join(litSpans(styledLine(t, m, "REL-11")), "|"); got != "rele" {
		t.Errorf("/rele lights %q, want rele", got)
	}

	// The group's name is no word of the line: the row is listed and lights nothing.
	press(m, tea.KeyEsc)
	typed(m, "/needs")
	if ids := strings.Join(shown(m), " "); ids != "REL-11" {
		t.Fatalf("/needs draws %q, want REL-11 alone", ids)
	}
	if got := litSpans(styledLine(t, m, "REL-11")); len(got) != 0 {
		t.Errorf("a word the line does not draw lit %q", got)
	}

	// A chart's row lights its name.
	press(m, tea.KeyEsc)
	typed(m, "/orbit-d")
	if got := strings.Join(litSpans(styledLine(t, m, "orbit-design")), "|"); got != "orbit-d" {
		t.Errorf("/orbit-d lights %q in the chart's row, want orbit-d", got)
	}

	// A title cut by a narrow layout lights what stays of it, and no letter
	// that was cut.
	press(m, tea.KeyEsc)
	m.width = 36
	typed(m, "/the")
	line = styledLine(t, m, "REL-11")
	text := stripANSI(line)
	for _, span := range litSpans(line) {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(span)) {
			t.Errorf("the lit span %q is not in the text that stays: %q", span, text)
		}
	}
	if ansi.StringWidth(line) != ansi.StringWidth(text) {
		t.Errorf("the lit line is %d cells wide, its text %d", ansi.StringWidth(line), ansi.StringWidth(text))
	}
	if width(text) > 36 {
		t.Fatalf("the row was not laid out narrow at 36 columns: %q", text)
	}

	// A model that styles nothing draws no code.
	plain := searchStore(t)
	typed(plain, "/rel 11")
	if line := styledLine(t, plain, "REL-11"); strings.Contains(line, "\x1b") {
		t.Errorf("a pipe's frame has style codes: %q", line)
	}
}

// TestLitLettersMoveNoColumn: a frame with the letters lit has the same text,
// on the same columns, as the frame without style, at 60, 100 and 230 columns.
func TestLitLettersMoveNoColumn(t *testing.T) {
	t.Parallel()
	for _, w := range []int{60, 100, 230} {
		for _, query := range []string{"rel 11", "the", "t", "orbit-d", "iss"} {
			styled, plain := searchStoreIn(t, styledConfig()), searchStore(t)
			for _, m := range []*Model{styled, plain} {
				m.width = w
				typed(m, "/"+query)
			}
			lit := styled.View()
			if got, want := stripANSI(lit), plain.View(); got != want {
				t.Errorf("%d columns, /%s: the frame lit differs from the plain one\n--- plain\n%s\n--- lit\n%s", w, query, want, got)
			}
			if !strings.Contains(lit, "\x1b[1;4m") && query != "orbit-d" {
				t.Errorf("%d columns, /%s: nothing is lit in the frame", w, query)
			}
		}
	}
}
