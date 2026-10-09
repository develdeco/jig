package verifydeliver

import (
	"encoding/json"
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

// backendFallback reports whether data, a session's result file, has the
// shape a backend gives the result it writes itself when a session wrote
// none: a JSON object with an "outcome" field, the slice result's, which a
// pick result does not have. It is the field, not any wording, that says so.
// outcome and summary are what the backend recorded, for the report; one that
// is not a string is left out.
func backendFallback(data []byte) (outcome, summary string, ok bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return "", "", false
	}
	if _, ok := top["outcome"]; !ok {
		return "", "", false
	}
	str := func(key string) string {
		var v string
		if raw, ok := top[key]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		return v
	}
	return str("outcome"), str("summary"), true
}
