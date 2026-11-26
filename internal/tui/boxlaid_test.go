package tui

// boxLaid adds the lines of a row the allocator laid out, with no cut marks:
// the page tests build rows from words.
func (p *page) boxLaid(ref *rowRef, lines []string) {
	p.boxRow(ref, laidRow{lines: lines})
}
