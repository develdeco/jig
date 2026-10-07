package termrec

import (
	"strings"
	"testing"
	"time"
)

// TestFinalTextDrawsWhatATerminalShows: the terminal model, read through the
// last frame as text, for what command-line programs write.
func TestFinalTextDrawsWhatATerminalShows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		width, height int
		out           string
		want          string
	}{
		{"plain text", 10, 4, "hello", "hello"},
		{"lines ended by CRLF", 10, 4, "a\r\nb\r\n", "a\nb"},
		{"a bare LF moves down, not back", 10, 4, "ab\ncd", "ab\n  cd"},
		{"CR returns to overwrite", 10, 4, "abc\rX", "Xbc"},
		{"a progress line redrawn in place", 10, 4, "10%\r\x1b[K100%", "100%"},
		{"erase to the end of the line", 10, 4, "abcdef\r\x1b[3C\x1b[K", "abc"},
		{"erase the whole line", 10, 4, "abcdef\x1b[2K", ""},
		{"backspace", 10, 4, "ab\bX", "aX"},
		{"tab stops every 8 columns", 20, 4, "a\tb", "a       b"},
		{"wraps at the right edge", 5, 4, "abcdefg", "abcde\nfg"},
		{"a full line then CRLF leaves no empty row", 5, 4, "abcde\r\nf", "abcde\nf"},
		{"scrolls past the bottom", 10, 2, "1\r\n2\r\n3", "2\n3"},
		{"cursor position", 10, 4, "\x1b[2;3HX", "\n  X"},
		{"an empty parameter keeps its place", 10, 4, "\x1b[;3HX", "  X"},
		{"cursor up, then overwrite", 10, 4, "a\r\nb\x1b[A\rZ", "Z\nb"},
		{"clear the screen", 10, 4, "abc\r\ndef\x1b[2J\x1b[HX", "X"},
		{"erase below the cursor", 10, 4, "abc\r\ndef\r\nghi\x1b[2;2H\x1b[J", "abc\nd"},
		{"colors draw no text", 10, 4, "\x1b[1;31mred\x1b[0m \x1b[38;5;196mx\x1b[38;2;1;2;3my\x1b[38:2::1:2:3mz", "red xyz"},
		{"a title (OSC, ended by BEL) draws nothing", 10, 4, "\x1b]0;title\x07ok", "ok"},
		{"a hyperlink (OSC, ended by ST) draws its text only", 10, 4, "\x1b]8;;http://example.invalid\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"mode switches draw nothing", 10, 4, "\x1b[?25lhi\x1b[?25h\x1b[?1049h", "hi"},
		{"a charset designation draws nothing", 10, 4, "\x1b(Bok", "ok"},
		{"save and restore the cursor", 10, 4, "ab\x1b7cd\x1b8X", "abXd"},
		{"delete characters", 10, 4, "abcdef\r\x1b[2P", "cdef"},
		{"insert characters", 10, 4, "abcd\r\x1b[2@", "  abcd"},
		{"erase characters", 10, 4, "abcdef\r\x1b[2X", "  cdef"},
		{"invalid UTF-8 draws a replacement character", 10, 4, "a\xffb", "a�b"},
		{"a wide rune takes two cells", 4, 4, "日本語", "日本\n語"},
		{"a wide rune that does not fit wraps whole", 3, 4, "ab日", "ab\n日"},
		{"overwriting the left half of a wide rune clears its right half", 10, 4, "日b\rX", "X b"},
		{"overwriting the right half of a wide rune clears its left half", 10, 4, "日b\r\x1b[CX", " Xb"},
		{"a combining mark joins the rune before it", 10, 4, "éx", "éx"},
		{"a reset clears everything", 10, 4, "abc\x1bcX", "X"},
		{"a DCS string draws nothing", 10, 4, "\x1bP+q544e\x1b\\ok", "ok"},
		{"an APC string draws nothing", 10, 4, "\x1b_Gf=100;AAAA\x1b\\ok", "ok"},
		{"a PM string draws nothing", 10, 4, "\x1b^privacy\x1b\\ok", "ok"},
		{"backspace from the last column", 5, 4, "abcde\bX", "abcXe"},
		{"erasing the scrollback leaves the screen", 10, 4, "abc\x1b[3J", "abc"},
		{"ESC inside an escape sequence starts it over", 10, 4, "\x1b\x1b[31mX", "X"},
		{"CAN cancels a sequence", 10, 4, "\x1b[31\x18X", "X"},
		{"a control inside an escape sequence runs", 10, 4, "abc\x1b\r[2CX", "abX"},
		{"reverse index at the top scrolls down", 10, 4, "a\x1bMb", " b\na"},
		{"insert lines", 10, 4, "a\r\nb\x1b[A\x1b[L", "\na\nb"},
		{"delete lines", 10, 4, "a\r\nb\r\nc\x1b[2A\x1b[M", "b\nc"},
		{"scroll up", 10, 4, "a\r\nb\x1b[S", "b"},
		{"scroll down", 10, 4, "a\x1b[T", "\na"},
		{"a 1x1 terminal shows the last rune", 1, 1, "abc", "c"},
		{"a mark after a pending wrap joins the last rune", 3, 2, "abć", "abć"},
		{"a cell keeps at most 8 marks", 10, 2, "e" + strings.Repeat("́", 20), "e" + strings.Repeat("́", 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Cast{Width: tc.width, Height: tc.height, Events: []Event{{Data: tc.out}}}.FinalText()
			if err != nil {
				t.Fatalf("FinalText: %v", err)
			}
			if got != tc.want {
				t.Errorf("FinalText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFinalTextCarriesASequenceSplitAcrossWrites: a program's write can end
// mid escape sequence or mid rune; the next write finishes it.
func TestFinalTextCarriesASequenceSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	c := Cast{Width: 10, Height: 2, Events: []Event{
		{Data: "\x1b["},
		{Time: time.Millisecond, Data: "31mre"},
		{Time: 2 * time.Millisecond, Data: "d\xe6\x97"},
		{Time: 3 * time.Millisecond, Data: "\xa5\x1b]0;ti"},
		{Time: 4 * time.Millisecond, Data: "tle\x07!"},
	}}
	got, err := c.FinalText()
	if err != nil {
		t.Fatalf("FinalText: %v", err)
	}
	if want := "red日!"; got != want {
		t.Errorf("FinalText = %q, want %q", got, want)
	}
}

// TestFinalTextReadsPastAnOverlongSequence: a CSI sequence with more
// parameter bytes than the model keeps is still read to its end, and the
// text after it is drawn.
func TestFinalTextReadsPastAnOverlongSequence(t *testing.T) {
	t.Parallel()
	c := Cast{Width: 10, Height: 2, Events: []Event{{Data: "\x1b[" + strings.Repeat("1;", 10000) + "mok"}}}
	got, err := c.FinalText()
	if err != nil {
		t.Fatalf("FinalText: %v", err)
	}
	if got != "ok" {
		t.Errorf("FinalText = %q, want %q", got, "ok")
	}
}
