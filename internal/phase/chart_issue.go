package phase

import (
	"fmt"
	"strings"
)

// A model chart (one that plans an issue, ADR 0064) stops at each landing: the
// person decides every merge into the issue's branch, the session makes it at
// "Land X", and the issue gets one suite run and one pull request after every
// ticket has landed. This file derives what that adds to the standing: the
// landings the run waits for, the multi-tasks to publish, and the issue's own
// stage. A legacy chart has none of it.

// acts says the issue's own stage is work of the run: the suite to run, a fix
// task to add, or the issue to publish.
func (i *ChartIssue) acts() bool {
	if i == nil {
		return false
	}
	switch i.Stage {
	case IssueSuite, IssueFix, IssuePublish:
		return true
	}
	return false
}

// modelRows lays Lands, Publish and Issue on c from the rows, in row order, the
// issue's own landing last.
func (d *chartDeriver) modelRows(c *Chart) {
	for _, r := range c.Rows {
		if r.ID == "" {
			continue
		}
		e := d.f.Entries[d.byPos[r.Pos]]
		switch {
		case e.Container != nil:
			switch e.Container.State {
			case TailReady:
				c.Publish = append(c.Publish, ChartPublish{ID: r.ID, Prompt: e.Container.Prompt})
			case TailPublished:
				c.Lands = append(c.Lands, ChartLand{Pos: r.Pos, ID: r.ID, Prompt: Prompt(Land, r.ID), After: e.After, State: "waits for a merge"})
			}
		case r.Kind == "" && r.Ticket != nil && r.Ticket.Current == Land && r.Ticket.Code == CodeDone:
			c.Lands = append(c.Lands, ChartLand{
				Pos: r.Pos, ID: r.ID, Prompt: Prompt(Land, r.ID), After: e.After,
				State: r.Ticket.Phases[len(r.Ticket.Phases)-1].Text,
			})
		}
	}
	if t := d.f.Tail; t != nil {
		c.Issue = d.issueStage(*t, len(c.Lands) > 0)
		if c.Issue.Stage == IssuePublished {
			c.Lands = append(c.Lands, ChartLand{ID: d.f.Issue, Prompt: Prompt(Land, d.f.Issue), State: "waits for a merge"})
		}
	}
}

// issueStage is where the issue itself stands. Its children not all landed
// (or a landing still to make) keep it at landing; once they have, the suite
// comes first, then a fix task after a failed one, then the publish.
func (d *chartDeriver) issueStage(t ChartTail, landsHeld bool) *ChartIssue {
	is := &ChartIssue{ID: d.f.Issue, Unmet: t.Unmet}
	if len(t.Unmet) > 0 {
		is.Code, is.Why, is.Prompt = t.Unmet[0].Code, t.Unmet[0].Why, t.Unmet[0].Prompt
	}
	switch {
	case t.State == TailLanded:
		is.Stage = IssueLanded
	case t.State == TailPublished:
		is.Stage, is.Prompt = IssuePublished, Prompt(Land, d.f.Issue)
	case t.State == TailReady:
		is.Stage, is.Prompt = IssuePublish, t.Prompt
		if is.Prompt == "" {
			is.Prompt = Prompt(Publish, d.f.Issue)
		}
	case len(t.Waits) > 0 || len(t.Unmet) == 0 || landsHeld:
		is.Stage, is.Code, is.Prompt = IssueLanding, "", ""
		if len(t.Waits) > 0 {
			is.Why = fmt.Sprintf("%s waits for %s to land", d.f.Issue, strings.Join(t.Waits, ", "))
		}
	default:
		is.Stage = IssueUnmet
		for _, u := range t.Unmet {
			if u.Code == CodeSuiteFailed {
				is.Stage, is.Code, is.Why, is.Prompt = IssueFix, u.Code, u.Why, u.Prompt
				return is
			}
		}
		for _, u := range t.Unmet {
			if u.Code == CodeSuiteNotRun {
				is.Stage, is.Code, is.Why, is.Prompt = IssueSuite, u.Code, u.Why, u.Prompt
				return is
			}
		}
	}
	return is
}

// landReason says what a landing stop waits for: the first landing that may be
// asked for now, then the others with what they wait after.
func (c Chart) landReason() string {
	first := c.Lands[0]
	for _, l := range c.Lands {
		if len(l.After) == 0 {
			first = l
			break
		}
	}
	out := fmt.Sprintf("%s waits for the person's word: %s", first.ID, first.Prompt)
	for _, l := range c.Lands {
		if l.ID == first.ID {
			continue
		}
		if len(l.After) > 0 {
			out += fmt.Sprintf("; then %s, after %s", l.Prompt, strings.Join(l.After, ", "))
		} else {
			out += "; also " + l.Prompt
		}
	}
	return out
}
