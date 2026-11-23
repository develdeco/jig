package tui

import (
	"strings"
	"testing"
)

var testColumns = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}, {gap: 2}, {free: true, gap: 2}}

// TestAllocatorSizesColumnsByContent: a measured column is as wide as its
// widest cell among the rows drawn, never less, and a free column that has room
// is as wide as its widest cell too, so the columns after it line up.
func TestAllocatorSizesColumnsByContent(t *testing.T) {
	t.Parallel()
	rows := [][]string{
		{"○", "A-1", "Short title", "● ○", "ok"},
		{"●", "LONGER-ID-22", "A title", "● ●", "done"},
	}
	got := layout(testColumns, rows, 100)
	want := []string{
		"○ A-1           Short title  ● ○  ok",
		"● LONGER-ID-22  A title      ● ●  done",
	}
	for i, w := range want {
		if len(got[i]) != 1 || got[i][0] != w {
			t.Errorf("row %d is %q, want one line %q", i, got[i], w)
		}
	}

	// A column no row has a cell for is not drawn, nor is its gap.
	rows = [][]string{{"○", "A-1", "", "● ○", ""}}
	if got := layout(testColumns, rows, 100); got[0][0] != "○ A-1  ● ○" {
		t.Errorf("a row with empty title and detail is %q", got[0])
	}
	// A row with fewer cells than columns is drawn with what it has.
	if got := layout(testColumns, [][]string{{"○", "A-1"}}, 100); got[0][0] != "○ A-1" {
		t.Errorf("a short row is %q", got[0])
	}
}

// TestAllocatorWrapsFreeTextUnderItsColumn: free text wraps at words onto
// rows aligned under its column, at most three; when it goes on, the last ends
// in an ellipsis. A word wider than the column is broken at the column, and a
// hyphen is not a place to wrap.
func TestAllocatorWrapsFreeTextUnderItsColumn(t *testing.T) {
	t.Parallel()
	cols := []column{{}, {free: true, gap: 2}}
	// 1 + 2 + 20: the free column is 20 cells.
	const w = 23

	got := layout(cols, [][]string{{"○", "one two three four five six"}}, w)[0]
	want := []string{"○  one two three four", "   five six"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapped text is %q, want %q", got, want)
	}

	long := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma"
	got = layout(cols, [][]string{{"○", long}}, w)[0]
	if len(got) != 3 {
		t.Fatalf("text past three rows takes %d rows: %q", len(got), got)
	}
	for i, l := range got {
		if width(l) > w {
			t.Errorf("row %d is %d cells: %q", i, width(l), l)
		}
		if i > 0 && !strings.HasPrefix(l, "   ") {
			t.Errorf("continuation row %d is not under its column: %q", i, l)
		}
	}
	if !strings.HasSuffix(got[2], "…") || strings.HasSuffix(got[0], "…") || strings.HasSuffix(got[1], "…") {
		t.Errorf("only the last row of cut text ends in an ellipsis: %q", got)
	}

	// Text that fits in three rows has no ellipsis.
	got = layout(cols, [][]string{{"○", "aaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbb cccccccccccccccccccc"}}, w)[0]
	if len(got) != 3 || strings.Contains(strings.Join(got, ""), "…") {
		t.Errorf("three whole rows are %q", got)
	}

	// A word wider than the column is broken at it.
	got = layout(cols, [][]string{{"○", strings.Repeat("w", 30)}}, w)[0]
	if len(got) != 2 || got[0] != "○  "+strings.Repeat("w", 20) || got[1] != "   "+strings.Repeat("w", 10) {
		t.Errorf("a word wider than the column is %q", got)
	}

	// A name with hyphens in it is moved whole to the next row.
	got = layout(cols, [][]string{{"○", "into 501-review-rounds"}}, w)[0]
	if len(got) != 2 || got[1] != "   501-review-rounds" {
		t.Errorf("a hyphenated name is %q", got)
	}

	// Color open on a cell is open on each row it wraps to, and closed at its end.
	painted := "\x1b[33mone two three four five six\x1b[0m"
	got = layout(cols, [][]string{{"○", painted}}, w)[0]
	if len(got) != 2 || !strings.Contains(got[1], "\x1b[33mfive six") || !strings.HasSuffix(got[0], "\x1b[0m") {
		t.Errorf("a colored cell wraps to %q", got)
	}
}

// TestAllocatorStacksFreeColumnsAndCutsMeasuredOnesLast: two free columns sit
// side by side when each gets 20 cells, else the second stacks under the first;
// measured columns are cut only when they and 20 cells leave no room, the
// widest that is not an id first and the ids last.
func TestAllocatorStacksFreeColumnsAndCutsMeasuredOnesLast(t *testing.T) {
	t.Parallel()
	title := "A title of about thirty chars"
	detail := "Build: 1 of 2 slices green"
	rows := [][]string{{"○", "JIG-1", title, "● ○ ○ ○ ○", detail}}

	// Room for both: side by side on one row.
	if got := layout(testColumns, rows, 100)[0]; len(got) != 1 || !strings.Contains(got[0], title+"  ● ○ ○ ○ ○  "+detail) {
		t.Errorf("a wide row is %q", got)
	}

	// 1+1+5 + 2+title + 2+9 + 2+detail would be 1+1+5+2+29+2+9+2+26=77; at 60 the
	// detail would get under 20, so it stacks under the title.
	got := layout(testColumns, rows, 60)[0]
	if len(got) < 2 {
		t.Fatalf("at 60 the detail stacks under the title: %q", got)
	}
	col := strings.Index(got[0], "A title")
	if col < 0 {
		t.Fatalf("the title is missing: %q", got)
	}
	col = width(got[0][:col])
	if last := got[len(got)-1]; !strings.HasPrefix(last, spaces(col)+"Build:") {
		t.Errorf("the stacked detail is not under the title's left edge: %q", got)
	}
	for _, l := range got {
		if width(l) > 60 {
			t.Errorf("a row is %d cells: %q", width(l), l)
		}
	}
	if !strings.Contains(got[0], "● ○ ○ ○ ○") {
		t.Errorf("the step dots stay on the row's first line: %q", got)
	}

	// The measured columns leave the free text 20 cells before any is cut: a
	// 40-cell tag is cut first, with an ellipsis, and the id stays whole.
	cols := []column{{id: true}, {gap: 2}, {free: true, gap: 2}}
	id := strings.Repeat("i", 24)
	tag := strings.Repeat("t", 40)
	got = layout(cols, [][]string{{id, tag, "free text of the row"}}, 70)[0]
	if len(got) != 1 || !strings.Contains(got[0], id) || !strings.Contains(got[0], "t…") || !strings.HasSuffix(got[0], "free text of the row") {
		t.Errorf("the tag gives way first: %q", got)
	}
	if width(got[0]) > 70 {
		t.Errorf("the row is %d cells", width(got[0]))
	}

	// With no room left to cut in the other columns, the id gives way, last.
	got = layout(cols, [][]string{{id, "tag", "free text of the row"}}, 46)[0]
	if want := strings.Repeat("i", 20) + "…  …  free text of the row"; len(got) != 1 || got[0] != want {
		t.Errorf("the id gives way last: %q, want %q", got, want)
	}
}
