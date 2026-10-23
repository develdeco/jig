package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// statusInfo is `jig status <ticket>`, read: the store's word on the ticket's
// slices, its questions and the asks a gate round left for a person.
type statusInfo struct {
	State     string // building, parked, stalled, env-blocked or green
	Branch    string
	Slices    []statusSlice
	Questions []statusQuestion
	Stalled   []statusStalled
	Asks      []statusAsk
	// Help is the status's own account of the next step.
	Help []string
	// Err is set when the status itself was refused.
	Err *axiErr
}

type statusSlice struct {
	ID, State, BlockedBy, Question string
	Attempts                       int
}

type statusQuestion struct{ ID, Slice, Status string }

type statusStalled struct{ Slice, Reason, Summary string }

type statusAsk struct{ ID, Risk, Where, Title, Why string }

// parseStatus reads what `jig status` printed. A refusal is an Err, and the
// rest of the info is empty.
func parseStatus(out string) *statusInfo {
	doc := parseAxi(out)
	info := &statusInfo{
		State:  doc.Scalars["state"],
		Branch: doc.Scalars["branch"],
		Help:   doc.Help,
		Err:    doc.Err,
	}
	if doc.Err == nil && doc.Scalars["ticket"] == "" {
		// Not a status at all: say what it was.
		info.Err = &axiErr{Msg: "jig status printed " + firstWords(out)}
	}
	if info.Err != nil {
		return info
	}
	dash := func(s string) string {
		if s == "-" {
			return ""
		}
		return s
	}
	for _, r := range doc.Tables["slices"].Maps() {
		info.Slices = append(info.Slices, statusSlice{
			ID: r["id"], State: r["state"], Attempts: atoi(r["attempts"]),
			BlockedBy: dash(r["blocked_by"]), Question: dash(r["question"]),
		})
	}
	for _, r := range doc.Tables["questions"].Maps() {
		info.Questions = append(info.Questions, statusQuestion{ID: r["id"], Slice: r["slice"], Status: r["status"]})
	}
	for _, r := range doc.Tables["stalled"].Maps() {
		info.Stalled = append(info.Stalled, statusStalled{Slice: r["slice"], Reason: r["reason"], Summary: dash(r["summary"])})
	}
	for _, r := range doc.Tables["outstanding_asks"].Maps() {
		info.Asks = append(info.Asks, statusAsk{ID: r["id"], Risk: r["risk"], Where: r["file:line"], Title: r["title"], Why: r["why"]})
	}
	return info
}

// parseRoster reads `jig next --json` for the store: the tickets, in the order
// the verb lists them. The verb may print a note before the object (a
// checkpoint line); the object is the first line that starts with a brace.
func parseRoster(out string) ([]rosterRow, error) {
	var obj string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			obj = line
			break
		}
	}
	if obj == "" {
		if doc := parseAxi(out); doc.Err != nil {
			return nil, fmt.Errorf("%s", doc.Err.Msg)
		}
		return nil, fmt.Errorf("jig next printed no ticket list")
	}
	var next struct {
		Tickets []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			State string `json:"state"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(obj), &next); err != nil {
		return nil, fmt.Errorf("jig next printed a ticket list that does not parse: %w", err)
	}
	rows := make([]rosterRow, 0, len(next.Tickets))
	for _, t := range next.Tickets {
		rows = append(rows, rosterRow{ID: t.ID, Title: t.Title, State: t.State})
	}
	return rows, nil
}

// firstWords is the start of what a verb printed, for a message about it.
func firstWords(out string) string {
	out = strings.Join(strings.Fields(out), " ")
	if out == "" {
		return "nothing"
	}
	if len(out) > 60 {
		out = out[:60] + "..."
	}
	return strconv.Quote(out)
}
