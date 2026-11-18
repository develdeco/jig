package verifydeliver

import (
	"strings"
)

// refusalReasonCap bounds a recorded refusal reason: an operating system
// error it quotes can run long, and a journal line is a record, not a log.
const refusalReasonCap = 400

// oneLine returns s as one line, with runs of whitespace collapsed.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// refusalReason renders err as a refusal reason for the journal and the
// report: one line, with runs of whitespace collapsed, capped at
// refusalReasonCap runes.
func refusalReason(err error) string {
	s := oneLine(err.Error())
	if r := []rune(s); len(r) > refusalReasonCap {
		s = string(r[:refusalReasonCap]) + "..."
	}
	return s
}
