package verify

import "strings"

// tailBytes is how much of an oracle's output the machine-local log keeps
// of a failed run.
const tailBytes = 4000

// summaryBytes is how much of a failure's tail the journal line carries.
const summaryBytes = 600

// outputTail keeps the last 4,000 bytes of an oracle's output: enough to
// show a failure, small enough for a log. (Copied from the frontier, which
// kept the same for a prompt and a summary.)
func outputTail(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > tailBytes {
		out = out[len(out)-tailBytes:]
	}
	return out
}

// oneLine flattens the end of s to one line of at most about 600 bytes, for
// a journal summary. The kept part starts at a line boundary, so two
// identical failures summarize identically and the stall counter can match
// them. (Copied from the frontier.)
func oneLine(s string) string {
	if len(s) > summaryBytes {
		s = s[len(s)-summaryBytes:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// normalizeLine is a line of oracle output as the baseline compares it:
// whitespace trimmed and every run of digits replaced by one "#", so timings,
// counts, ports and line numbers do not make a line new.
func normalizeLine(l string) string {
	l = strings.TrimSpace(l)
	var b strings.Builder
	inDigits := false
	for _, r := range l {
		if r >= '0' && r <= '9' {
			if !inDigits {
				b.WriteByte('#')
			}
			inDigits = true
			continue
		}
		inDigits = false
		b.WriteRune(r)
	}
	return b.String()
}

// newLines returns the lines of out, trimmed and in printed order, whose
// normalized form is not in known.
func newLines(out string, known map[string]bool) []string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !known[normalizeLine(l)] {
			keep = append(keep, l)
		}
	}
	return keep
}

// headBytes joins lines with newlines, keeping whole lines while they fit in
// max bytes; the first line is cut to max when it alone does not fit. Whole
// lines keep two identical failures summarizing identically.
func headBytes(lines []string, max int) string {
	var b strings.Builder
	for _, l := range lines {
		if b.Len() > 0 {
			if b.Len()+1+len(l) > max {
				break
			}
			b.WriteByte('\n')
		} else if len(l) > max {
			return l[:max]
		}
		b.WriteString(l)
	}
	return b.String()
}

// markerOldTail separates the new lines from today's last bytes in a failed
// run's tail.
const markerOldTail = "--- the last bytes of the output ---"

// failureOutput is a failed run's tail and summary. With a baseline (the
// normalized lines of the command's latest pass) the tail is the lines the
// failed run printed that the pass did not, at most 4,000 bytes of them, then
// a marker and today's tail, and the summary the first ~600 bytes of those
// lines on one line. With no baseline, or no new line, they are today's.
func failureOutput(out string, err error, baseline map[string]bool) (tail, summary string) {
	old := failureTail(out, err)
	if baseline == nil {
		return old, oneLine(old)
	}
	fresh := newLines(out, baseline)
	if len(fresh) == 0 {
		return old, oneLine(old)
	}
	// A run the wedge guard killed says so in today's tail; the reason is
	// not in the output, so it follows the new lines to reach the summary
	// and the signature.
	if strings.Contains(err.Error(), "did not finish within") {
		fresh = append(fresh, err.Error())
	}
	tail = headBytes(fresh, tailBytes) + "\n" + markerOldTail + "\n" + old
	return tail, strings.Join(strings.Fields(headBytes(fresh, summaryBytes)), " ")
}

// baselineText is the file body for a passing run's output: its distinct
// normalized lines, one per line.
func baselineText(out string) string {
	seen := map[string]bool{}
	var b strings.Builder
	for _, l := range strings.Split(out, "\n") {
		n := normalizeLine(l)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		b.WriteString(n)
		b.WriteByte('\n')
	}
	return b.String()
}

// parseBaseline reads a baseline file's body into a set; nil when there is
// none to compare against.
func parseBaseline(body string) map[string]bool {
	set := map[string]bool{}
	for _, l := range strings.Split(body, "\n") {
		if l != "" {
			set[l] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
