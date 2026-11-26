package tui

// layout draws rows of cells under cols in width cells with free cells held to
// wrapRows rows, as the lines of each row: the allocator without its marks.
func layout(cols []column, rows [][]string, width int) [][]string {
	laid := layoutMarked(cols, rows, width, wrapRows, nil)
	out := make([][]string, len(laid))
	for i, r := range laid {
		out[i] = r.lines
	}
	return out
}

// cut says whether the row holds a cut.
func (r laidRow) cut() bool {
	for _, c := range r.cuts {
		if len(c) > 0 {
			return true
		}
	}
	return false
}
