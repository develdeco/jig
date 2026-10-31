package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A chart reaches the screens through two reads, both verbs: the store listing
// (`jig next --json`) names each chart and the ids of the tickets it
// graduated, which is all the project's list needs; `jig next <chart> --json`
// is the chart screen's data, the chart's standing as internal/phase derives it
// (the order of its tickets, what waits for what, what a run would do) and its
// map. The screens derive none of that themselves.

// chartListing is a chart as the store listing names it.
type chartListing struct {
	Name, Title string
	// Tickets are the ids of the entries that have a ticket, in the chart's
	// order; Ungraduated counts the ones that have none yet.
	Tickets     []string
	Ungraduated int
}

// chartInfo is `jig next <chart> --json`, read.
type chartInfo struct {
	Name, Title string
	// Ready, Code, Reason and Prompt are the chart's verdict: whether a run can
	// start or go on, and if not why, and what to ask a session to run it.
	Ready                bool
	Code, Reason, Prompt string
	Started              bool
	Ungraduated          int
	// Briefs are the ids the run briefs first; Run are the ids it builds, in
	// waves.
	Briefs  []string
	Run     [][]string
	Tickets []chartTicket
	// Ends are the tickets the run stops at, each behind a merge.
	Ends []chartEnd
	// Map is the chart's map.md; nil when it has none.
	Map *chartMap
}

// chartTicket is one entry of a chart: a ticket, or (ID "") an entry that has
// none yet.
type chartTicket struct {
	ID, Title string
	// Phase is the phase the ticket is at (brief, build, review or publish),
	// Steps the four phases' states (done, current or pending), and Ready whether
	// Phase can start now.
	Phase   string
	Steps   []string
	Ready   bool
	Problem string
	// Waits are the needs of the ticket that are not met.
	Waits []chartWait
}

// chartWait is a need of a ticket that is not met: the ticket it is on and in
// what way (merged or stacked).
type chartWait struct {
	Ticket, Kind, Fact string
	PRs                []chartPR
}

// chartPR is a pull request a chart waits for.
type chartPR struct{ Repo, URL, State string }

// chartEnd is where a run stops: Ticket waits for Blocker to merge.
type chartEnd struct {
	Ticket, Blocker, Kind, Fact string
	PRs                         []chartPR
}

// chartMap is the sections of a chart's map.md the screen shows.
type chartMap struct {
	Destination, Decided, Fog, OutOfScope []string
}

// chartMsg is a chart's verb output, read.
type chartMsg struct {
	name string
	info *chartInfo
	err  error
}

// parseChart reads what `jig next <chart> --json` printed. Output that is not
// the chart's JSON (nothing, a refusal, text, a ticket's document, an older jig
// that does not know a chart) is no chart and no error: the screen then has
// only the listing. A JSON object of the wrong shape is an error.
func parseChart(out string) (*chartInfo, error) {
	var obj string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			obj = line
			break
		}
	}
	if obj == "" {
		return nil, nil
	}
	var v struct {
		Chart       string     `json:"chart"`
		Title       string     `json:"title"`
		Ready       bool       `json:"ready"`
		Code        string     `json:"code"`
		Reason      string     `json:"reason"`
		Prompt      string     `json:"prompt"`
		Started     bool       `json:"started"`
		Ungraduated int        `json:"ungraduated"`
		Briefs      []string   `json:"briefs"`
		Run         [][]string `json:"run"`
		Tickets     []struct {
			ID      string   `json:"id"`
			Title   string   `json:"title"`
			Phase   string   `json:"phase"`
			Steps   []string `json:"steps"`
			Ready   bool     `json:"ready"`
			Problem string   `json:"problem"`
			Waits   []struct {
				Ticket string    `json:"ticket"`
				Kind   string    `json:"kind"`
				Fact   string    `json:"fact"`
				PRs    []chartPR `json:"prs"`
			} `json:"waits_for"`
		} `json:"tickets"`
		Ends []struct {
			Ticket  string    `json:"ticket"`
			Blocker string    `json:"blocker"`
			Kind    string    `json:"kind"`
			Fact    string    `json:"fact"`
			PRs     []chartPR `json:"prs"`
		} `json:"ends"`
		Map *struct {
			Destination []string `json:"destination"`
			Decided     []string `json:"decided"`
			Fog         []string `json:"fog"`
			OutOfScope  []string `json:"out_of_scope"`
		} `json:"map"`
	}
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, nil
		}
		return nil, fmt.Errorf("jig next printed a chart that does not parse: %w", err)
	}
	if v.Chart == "" {
		return nil, nil
	}
	info := &chartInfo{
		Name: v.Chart, Title: v.Title, Ready: v.Ready, Code: v.Code, Reason: v.Reason, Prompt: v.Prompt,
		Started: v.Started, Ungraduated: v.Ungraduated, Briefs: v.Briefs, Run: v.Run,
	}
	for _, t := range v.Tickets {
		ct := chartTicket{ID: t.ID, Title: t.Title, Phase: t.Phase, Steps: t.Steps, Ready: t.Ready, Problem: t.Problem}
		for _, w := range t.Waits {
			ct.Waits = append(ct.Waits, chartWait{Ticket: w.Ticket, Kind: w.Kind, Fact: w.Fact, PRs: w.PRs})
		}
		info.Tickets = append(info.Tickets, ct)
	}
	for _, e := range v.Ends {
		info.Ends = append(info.Ends, chartEnd{Ticket: e.Ticket, Blocker: e.Blocker, Kind: e.Kind, Fact: e.Fact, PRs: e.PRs})
	}
	if v.Map != nil {
		info.Map = &chartMap{Destination: v.Map.Destination, Decided: v.Map.Decided, Fog: v.Map.Fog, OutOfScope: v.Map.OutOfScope}
	}
	return info, nil
}
