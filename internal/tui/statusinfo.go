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
	// Brief is the brief's approval and sections; nil when the ticket has no
	// brief (or the status predates the block).
	Brief *statusBrief
	// FixBudget is the status's fix_budget line ("1 of 3 rounds used"), ""
	// before the ticket's first gate round.
	FixBudget string
	// Rounds are the recorded gate rounds, oldest first; Findings are what the
	// latest one raised. Publish is what a publish has written, nil before it
	// began. All three are empty for a status that predates their sections.
	Rounds   []statusRound
	Findings []statusFinding
	Publish  *statusPublish
	// Help is the status's own account of the next step.
	Help []string
	// Err is set when the status itself was refused.
	Err *axiErr
}

type statusSlice struct {
	ID, State, BlockedBy, Question, Goal string
	// Workspace and Oracle are what slices.yaml names for the slice (the oracle
	// as written there: a manifest oracle's name, or a command); FromGate is the
	// round a fix slice came from, 0 for any other.
	Workspace, Oracle string
	FromGate          int
	Attempts          int
}

// statusRound is a row of the status's rounds table: a recorded gate round, who
// reported it and what it reviewed. A cell the status did not know is "".
type statusRound struct {
	N                                 int
	Verdict, Reviewer, Harness, Model string
	Independent                       string // true, false or unknown
	Fix, Ask, Note                    int
	Scope, Base, Head                 string
	Files                             int // -1 when the round has no findings.yaml
}

// statusFinding is a row of the status's findings table: a finding the latest
// round raised, and the decision pending for it.
type statusFinding struct {
	ID                                 string
	Round                              int
	Action, Risk, Where, Title, Status string
	Rationale, Decision, DecidedBy     string
}

// statusPublish is the status's publish block: what the publish has written.
// Body and Changelog are the first lines of the files named beside them.
type statusPublish struct {
	PR, PRState, Head, Branch string
	BodyFile, Body            string
	ChangelogFile, Changelog  string
}

// statusBrief is the brief block of a status: whether the brief as it is now
// is the approved one (none, current or changed), who approved it, when and
// how, and its sections with the slices that cite each.
type statusBrief struct {
	Approval, ApprovedBy, ApprovedAt, Provenance string
	Sections                                     []statusBriefSection
}

type statusBriefSection struct {
	Heading string
	Slices  []string
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
			BlockedBy: dash(r["blocked_by"]), Question: dash(r["question"]), Goal: dash(r["goal"]),
			Workspace: dash(r["workspace"]), Oracle: dash(r["oracle"]), FromGate: atoi(r["from_gate"]),
		})
	}
	info.FixBudget = doc.Scalars["fix_budget"]
	for _, r := range doc.Tables["rounds"].Maps() {
		files := -1
		if f := dash(r["files"]); f != "" {
			files = atoi(f)
		}
		info.Rounds = append(info.Rounds, statusRound{
			N: atoi(r["round"]), Verdict: r["verdict"], Reviewer: dash(r["reviewer"]), Harness: dash(r["harness"]), Model: dash(r["model"]),
			Independent: dash(r["independent"]), Fix: atoi(r["fix"]), Ask: atoi(r["ask"]), Note: atoi(r["note"]),
			Scope: dash(r["scope"]), Base: dash(r["base"]), Head: dash(r["head"]), Files: files,
		})
	}
	for _, r := range doc.Tables["findings"].Maps() {
		info.Findings = append(info.Findings, statusFinding{
			ID: r["id"], Round: atoi(r["round"]), Action: r["action"], Risk: r["risk"], Where: r["file:line"], Title: r["title"],
			Status: r["status"], Rationale: r["rationale"], Decision: r["decision"], DecidedBy: r["decided_by"],
		})
	}
	if kv, ok := doc.KV["publish"]; ok {
		info.Publish = &statusPublish{
			PR: kv["pr"], PRState: kv["pr_state"], Head: kv["head"], Branch: kv["branch"],
			BodyFile: kv["body_file"], Body: kv["body"], ChangelogFile: kv["changelog_file"], Changelog: kv["changelog"],
		}
	}
	for _, r := range doc.Tables["questions"].Maps() {
		info.Questions = append(info.Questions, statusQuestion{ID: r["id"], Slice: r["slice"], Status: r["status"]})
	}
	for _, r := range doc.Tables["stalled"].Maps() {
		info.Stalled = append(info.Stalled, statusStalled{Slice: r["slice"], Reason: r["reason"], Summary: dash(r["summary"])})
	}
	if kv, ok := doc.KV["brief"]; ok {
		b := &statusBrief{Approval: kv["approval"], ApprovedBy: kv["approved_by"], ApprovedAt: kv["approved_at"], Provenance: kv["provenance"]}
		for _, r := range doc.Tables["brief_sections"].Maps() {
			var cited []string
			if c := dash(r["slices"]); c != "" {
				cited = strings.Split(c, "+")
			}
			b.Sections = append(b.Sections, statusBriefSection{Heading: r["heading"], Slices: cited})
		}
		info.Brief = b
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
