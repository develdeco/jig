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
