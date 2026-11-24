package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// statusInfo is `jig status <ticket>`, read: the store's word on the ticket's
// slices, its questions and the asks a gate round left for a person.
type statusInfo struct {
	State  string // new, queued, building, parked, stalled, env-blocked, green or closed
	Branch string
	Closed string // how a closed ticket was closed, such as "covered by develdeco/jig#82"; "" when open
	Slices []statusSlice
	// LastRuns are the status's last_runs table by slice: each slice's last
	// witnessed run and whether it covers the slice's last commit.
	LastRuns map[string]statusLastRun
	// Verifications are the status's verification_states table: each commit
	// of a slice whose verification is not a pass, in the words internal/phase
	// tells it in and the command that clears it. A commit with no row is
	// verified, as far as the status says.
	Verifications []statusVerification
	Questions     []statusQuestion
	Stalled       []statusStalled
	Asks          []statusAsk
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
	// Probes, ProbeSkips and Unprobed are the status's ledger tables: every
	// probe and skip the rounds recorded, and the hunks the latest round that
	// recorded them left. Empty for a status that predates them.
	Probes     []statusProbe
	ProbeSkips []statusSkip
	Unprobed   []statusHunk
	// Need is the body of an issue or a multi-task, whole, and Suite an issue's
	// suite by repo: what the plan screen reads. Both are empty for a task.
	Need  string
	Suite []statusSuite
	// Standing, Prompt, Waits and Unmet are an issue's or multi-task's own pull
	// request as `jig next` derives it: waiting, ready, published or landed, what
	// to say next, the children not landed and what else is unmet.
	Standing, Prompt string
	Waits            []statusWait
	Unmet            []statusUnmet
	// Err is set when the status itself was refused.
	Err *axiErr
}

// statusWait is a child of a container that has not landed, with its standing.
type statusWait struct{ Ticket, Standing string }

// statusUnmet is a precondition of a container's publish that is not met.
type statusUnmet struct{ Code, Why, Prompt string }

// statusSuite is a row of an issue status's suite table: where a repo's suite
// stands at a tip of the issue's branch, and the tasks begun after it failed.
type statusSuite struct {
	Repo, Standing, Command, Tip string
	Seconds                      int
	Signature, Waiver, Why       string
	Fixes                        []string
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

// statusLastRun is a row of the status's last_runs table in its own words:
// Result is pass, fail, stopped or timed out, and Covers is yes, "no: <why>" or
// not checked.
type statusLastRun struct{ Result, Covers string }

// statusRound is a row of the status's rounds table: a recorded gate round, who
// reported it and what it reviewed. A cell the status did not know is "".
type statusRound struct {
	N                                 int
	Verdict, Reviewer, Harness, Model string
	Independent                       string // true, false or unknown
	Fix, Ask, Note                    int
	Scope, Base, Head                 string
	Files                             int // -1 when the round has no findings.yaml
	// Probes, Survived, Skipped and Unprobed are the round's ledger counts, -1
	// where the status did not say (a round no reviewer ran, a status that
	// predates the columns, and for Unprobed a round that did not record it).
	Probes, Survived, Skipped, Unprobed int
}

// statusProbe is a row of the status's probes table, Where being file:line.
type statusProbe struct {
	Round                                   int
	Outcome, Where, Seam, Mutation, Command string
}

// statusSkip is a row of the status's probe_skips table; Range is from-to or
// "whole file".
type statusSkip struct {
	Round            int
	File, Range, Why string
}

// statusHunk is a row of the status's unprobed table.
type statusHunk struct {
	Round    int
	File     string
	From, To int
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

// statusVerification is a row of the status's verification_states table. Commit
// is the sha the table shortens to twelve characters; State is the words
// internal/phase tells the state in, with a failed run's first FAIL line or a
// could-not-run summary after its colon; Clear is "" when the row names none.
type statusVerification struct{ Slice, Commit, State, Clear string }

// verificationOf is the row for the slice's commit sha, if the status has one.
func (s *statusInfo) verificationOf(slice, sha string) (statusVerification, bool) {
	if s == nil {
		return statusVerification{}, false
	}
	for _, v := range s.Verifications {
		if v.Slice == slice && v.Commit != "" && strings.HasPrefix(sha, v.Commit) {
			return v, true
		}
	}
	return statusVerification{}, false
}

// held is whether the state holds the builder at its stop: the commit has no
// recorded result, which running its verification puts right.
func (v statusVerification) held() bool { return v.State == string(phase.VerifyNoResult) }

// failedRun is whether the state says the code failed its oracle, as against a
// run that judged nothing or one that never started.
func (v statusVerification) failedRun() bool {
	return v.State == string(phase.VerifyFailed) || strings.HasPrefix(v.State, string(phase.VerifyFailed)+": ")
}

// icon is the state the verification is drawn in, the slice's: a state that
// holds the builder is stalled, a failed one failed, the rest drawn back.
func (v statusVerification) icon() string {
	switch {
	case v.held():
		return StateStalled
	case v.failedRun():
		return StateFail
	}
	return StateQueued
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
	for _, r := range doc.Tables["last_runs"].Maps() {
		if info.LastRuns == nil {
			info.LastRuns = map[string]statusLastRun{}
		}
		info.LastRuns[r["slice"]] = statusLastRun{Result: r["result"], Covers: r["covers"]}
	}
	info.Need = doc.Scalars["need"]
	info.Standing, info.Prompt = doc.Scalars["standing"], doc.Scalars["prompt"]
	for _, r := range doc.Tables["waits"].Maps() {
		info.Waits = append(info.Waits, statusWait{Ticket: r["ticket"], Standing: r["standing"]})
	}
	for _, r := range doc.Tables["unmet"].Maps() {
		info.Unmet = append(info.Unmet, statusUnmet{Code: r["code"], Why: r["why"], Prompt: r["prompt"]})
	}
	for _, r := range doc.Tables["suite"].Maps() {
		row := statusSuite{
			Repo: r["repo"], Standing: r["standing"], Command: dash(r["command"]), Tip: dash(r["tip"]), Seconds: atoi(dash(r["seconds"])),
			Signature: dash(r["signature"]), Waiver: dash(r["waiver"]), Why: dash(r["why"]),
		}
		if f := dash(r["fixes"]); f != "" {
			row.Fixes = strings.Split(f, "+")
		}
		info.Suite = append(info.Suite, row)
	}
	for _, r := range doc.Tables["verification_states"].Maps() {
		info.Verifications = append(info.Verifications, statusVerification{Slice: r["slice"], Commit: r["commit"], State: r["state"], Clear: dash(r["clear"])})
	}
	info.FixBudget = doc.Scalars["fix_budget"]
	if kv, ok := doc.KV["closed"]; ok {
		info.Closed = closedPhrase(kv["reason"], kv["by"])
	}
	for _, r := range doc.Tables["rounds"].Maps() {
		files := -1
		if f := dash(r["files"]); f != "" {
			files = atoi(f)
		}
		info.Rounds = append(info.Rounds, statusRound{
			N: atoi(r["round"]), Verdict: r["verdict"], Reviewer: dash(r["reviewer"]), Harness: dash(r["harness"]), Model: dash(r["model"]),
			Independent: dash(r["independent"]), Fix: atoi(r["fix"]), Ask: atoi(r["ask"]), Note: atoi(r["note"]),
			Scope: dash(r["scope"]), Base: dash(r["base"]), Head: dash(r["head"]), Files: files,
			Probes: count(r["probes"]), Survived: count(r["survived"]), Skipped: count(r["skipped"]), Unprobed: count(r["unprobed"]),
		})
	}
	for _, r := range doc.Tables["probes"].Maps() {
		info.Probes = append(info.Probes, statusProbe{
			Round: atoi(r["round"]), Outcome: r["outcome"], Where: r["file:line"], Seam: r["seam"], Mutation: r["mutation"], Command: r["command"],
		})
	}
	for _, r := range doc.Tables["probe_skips"].Maps() {
		info.ProbeSkips = append(info.ProbeSkips, statusSkip{Round: atoi(r["round"]), File: r["file"], Range: r["range"], Why: r["why"]})
	}
	for _, r := range doc.Tables["unprobed"].Maps() {
		info.Unprobed = append(info.Unprobed, statusHunk{Round: atoi(r["round"]), File: r["file"], From: atoi(r["from"]), To: atoi(r["to"])})
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
		b := &statusBrief{Approval: kv["approval"], ApprovedBy: firstOf(kv["identity"], bareIdentity(kv["approved_by"])), ApprovedAt: kv["approved_at"], Provenance: kv["provenance"]}
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

// count is a ledger cell as a number, -1 for a cell the status left "-" or did
// not have.
func count(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return -1
	}
	return atoi(s)
}

// closedPhrase is how a ticket was closed, in the words of its closure:
// "covered by develdeco/jig#82", "superseded by SHOP-4", "not planned".
func closedPhrase(reason, by string) string {
	switch {
	case reason == "not-planned":
		return "not planned"
	case by != "":
		return reason + " by " + by
	}
	return reason
}

// parseRoster reads `jig next --json` for the store: the tickets, in the order
// the verb lists them, each with the blockers that are not met, and the charts
// the store has. The verb may print a note before the object (a checkpoint
// line); the object is the first line that starts with a brace.
func parseRoster(out string) ([]rosterRow, []chartListing, error) {
	var obj string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			obj = line
			break
		}
	}
	if obj == "" {
		if doc := parseAxi(out); doc.Err != nil {
			return nil, nil, fmt.Errorf("%s", doc.Err.Msg)
		}
		return nil, nil, fmt.Errorf("jig next printed no ticket list")
	}
	var next struct {
		Tickets []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			State     string `json:"state"`
			BlockedBy []struct {
				Ticket string    `json:"ticket"`
				Kind   string    `json:"kind"`
				Fact   string    `json:"fact"`
				PRs    []chartPR `json:"prs"`
			} `json:"blocked_by"`
			Digest *struct {
				Kind         string   `json:"kind"`
				Parent       string   `json:"parent"`
				Current      string   `json:"current"`
				Ready        bool     `json:"ready"`
				Behind       bool     `json:"behind"`
				PhaseProblem string   `json:"phase_problem"`
				State        string   `json:"state"`
				Closed       string   `json:"closed"`
				LandFirst    []string `json:"land_first"`
				Unfinished   []string `json:"unfinished"`
				Children     []struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"children"`
				Phases []struct {
					Name     string `json:"name"`
					State    string `json:"state"`
					Text     string `json:"text"`
					NeedsYou bool   `json:"needs_you"`
					SubDone  bool   `json:"sub_done"`
					Changed  bool   `json:"changed"`
				} `json:"phases"`
				PRs []struct {
					Repo   string `json:"repo"`
					Number int    `json:"number"`
					URL    string `json:"url"`
					State  string `json:"state"`
					Target string `json:"target"`
				} `json:"prs"`
				Slices *struct {
					Total      int `json:"total"`
					Green      int `json:"green"`
					Building   int `json:"building"`
					Queued     int `json:"queued"`
					NeedsInput int `json:"needs_input"`
					Failed     int `json:"failed"`
					Stopped    int `json:"stopped"`
					Stalled    int `json:"stalled"`
					EnvBlocked int `json:"env_blocked"`
				} `json:"slices"`
			} `json:"digest"`
		} `json:"tickets"`
		Next   []string `json:"next"`
		Charts []struct {
			Name        string   `json:"name"`
			Title       string   `json:"title"`
			Tickets     []string `json:"tickets"`
			Ungraduated int      `json:"ungraduated"`
			Issue       string   `json:"issue"`
		} `json:"charts"`
	}
	if err := json.Unmarshal([]byte(obj), &next); err != nil {
		return nil, nil, fmt.Errorf("jig next printed a ticket list that does not parse: %w", err)
	}
	rows := make([]rosterRow, 0, len(next.Tickets))
	startAt := make(map[string]int, len(next.Next))
	for i, id := range next.Next {
		startAt[id] = i + 1
	}
	for _, t := range next.Tickets {
		row := rosterRow{ID: t.ID, Title: t.Title, State: t.State, StartOrder: startAt[t.ID]}
		for _, b := range t.BlockedBy {
			row.Blockers = append(row.Blockers, chartWait{Ticket: b.Ticket, Kind: b.Kind, Fact: b.Fact, PRs: b.PRs})
		}
		if g := t.Digest; g != nil {
			d := &digestInfo{Kind: g.Kind, Parent: g.Parent, Current: g.Current, Ready: g.Ready, Behind: g.Behind, PhaseProblem: g.PhaseProblem, State: g.State, Closed: g.Closed, LandFirst: g.LandFirst, Unfinished: g.Unfinished}
			for _, p := range g.Phases {
				d.Phases = append(d.Phases, digestPhase{Name: p.Name, State: p.State, Text: p.Text, NeedsYou: p.NeedsYou, SubDone: p.SubDone, Changed: p.Changed})
			}
			for _, p := range g.PRs {
				d.PRs = append(d.PRs, digestPR{Repo: p.Repo, Number: p.Number, URL: p.URL, State: p.State, Target: p.Target})
			}
			for _, c := range g.Children {
				d.Children = append(d.Children, digestChild{ID: c.ID, Title: c.Title})
			}
			if s := g.Slices; s != nil {
				d.Slices = &digestSlices{Total: s.Total, Green: s.Green, Building: s.Building, Queued: s.Queued, NeedsInput: s.NeedsInput, Failed: s.Failed, Stopped: s.Stopped, Stalled: s.Stalled, EnvBlocked: s.EnvBlocked}
			}
			row.Digest = d
		}
		rows = append(rows, row)
	}
	var charts []chartListing
	for _, c := range next.Charts {
		charts = append(charts, chartListing{Name: c.Name, Title: c.Title, Tickets: c.Tickets, Ungraduated: c.Ungraduated, Issue: c.Issue})
	}
	return rows, charts, nil
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

// bareIdentity is the identity inside an approved_by a status already worded
// ("an agent (as Dana)" is Dana), so a reader that words an approval itself
// (phase.Approver) never words it twice. A value that is not worded is its own
// identity.
func bareIdentity(by string) string {
	if inner, ok := strings.CutPrefix(by, "an agent (as "); ok {
		if inner, ok := strings.CutSuffix(inner, ")"); ok {
			return inner
		}
	}
	return by
}
