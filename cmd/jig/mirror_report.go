package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror"
)

// renderSyncReport prints report's user-visible outcome through w: what
// brief.md#jig trackers sync lists - "created, updated and reopened or
// closed issues, links added and removed, fields set, and every drift
// line" - plus whatever a publish-safety hit skipped or a missing issue
// left unlinked. Both `jig trackers sync` and the checkpoint hook wired
// into every other command (resolveStore) render the same report through
// this one function, so the two places brief.md#Ownership and drift names
// ("drift lines print in the output of the command whose checkpoint found
// them, and in jig trackers sync's") print the same shape. A table with no
// rows is left out, so a checkpoint whose sync changed nothing prints
// nothing. dryRun labels the created table "would_create" rather than
// "created", the only difference between a checkpoint's and a dry run's
// report. It returns true when report.Skipped is non-empty, the signal jig
// trackers sync exits non-zero on.
func renderSyncReport(w io.Writer, report mirror.SyncReport, dryRun bool) bool {
	if report.NoTracker {
		return false
	}

	createdLabel := "created"
	if dryRun {
		createdLabel = "would_create"
	}
	if len(report.Created) > 0 {
		rows := make([][]string, len(report.Created))
		for i, c := range report.Created {
			number := "pending"
			if c.Number != 0 {
				number = fmt.Sprintf("%d", c.Number)
			}
			rows[i] = []string{c.What, c.Owner + "/" + c.Repo, number}
		}
		axi.Render(w, axi.Table(createdLabel, []string{"what", "repo", "issue"}, rows))
	}

	if len(report.Recreated) > 0 {
		rows := make([][]string, len(report.Recreated))
		for i, c := range report.Recreated {
			rows[i] = []string{c.What, c.Owner + "/" + c.Repo, fmt.Sprintf("%d", c.Number)}
		}
		axi.Render(w, axi.Table("recreated", []string{"what", "repo", "issue"}, rows))
	}

	if len(report.Updated) > 0 {
		rows := make([][]string, len(report.Updated))
		for i, u := range report.Updated {
			rows[i] = []string{u.What, strings.Join(u.Fields, ",")}
		}
		axi.Render(w, axi.Table("updated", []string{"what", "fields"}, rows))
	}

	if len(report.Drift) > 0 {
		rows := make([][]string, len(report.Drift))
		for i, d := range report.Drift {
			rows[i] = []string{d.What, fmt.Sprintf("%d", d.Issue), d.Field}
		}
		axi.Render(w, axi.Table("drift", []string{"what", "issue", "field"}, rows))
	}

	// Every link this sync added, parents and blocked-by edges in one
	// table; a link it *removed* is drift by definition (an extra
	// sub-issue, a hand-added blocker) and prints above instead.
	if len(report.LinkedParents) > 0 || len(report.LinkedBlockedBy) > 0 {
		rows := make([][]string, 0, len(report.LinkedParents)+len(report.LinkedBlockedBy))
		for _, l := range report.LinkedParents {
			rows = append(rows, []string{l.Ticket, "parent", l.Chart})
		}
		for _, l := range report.LinkedBlockedBy {
			rows = append(rows, []string{l.Ticket, "blocked_by", l.Blocker})
		}
		axi.Render(w, axi.Table("linked", []string{"ticket", "link", "target"}, rows))
	}

	// The board's own "fields set": every item this sync placed or whose
	// Status it rewrote, and every pull request it put on the project.
	if len(report.Placed) > 0 || len(report.PlacedPRs) > 0 {
		rows := make([][]string, 0, len(report.Placed)+len(report.PlacedPRs))
		for _, p := range report.Placed {
			rows = append(rows, []string{p.What, p.Status})
		}
		for _, p := range report.PlacedPRs {
			rows = append(rows, []string{fmt.Sprintf("%s/%s#%d", p.Owner, p.Repo, p.Number), p.Status})
		}
		axi.Render(w, axi.Table("placed", []string{"item", "status"}, rows))
	}

	if len(report.NotLinked) > 0 {
		rows := make([][]string, len(report.NotLinked))
		for i, n := range report.NotLinked {
			rows[i] = []string{n.Ticket, n.Blocker}
		}
		axi.Render(w, axi.Table("not_linked", []string{"ticket", "blocker"}, rows))
	}

	if len(report.GoneRecordedPRs) > 0 {
		rows := make([][]string, len(report.GoneRecordedPRs))
		for i, g := range report.GoneRecordedPRs {
			rows[i] = []string{g.Ticket, fmt.Sprintf("%s/%s#%d", g.Owner, g.Repo, g.Number)}
		}
		axi.Render(w, axi.Table("pr_not_found", []string{"ticket", "pull request"}, rows))
	}

	if len(report.Skipped) > 0 {
		rows := make([][]string, len(report.Skipped))
		for i, s := range report.Skipped {
			rows[i] = []string{s.What, fmt.Sprintf("%d", s.Line), s.Text}
		}
		axi.Render(w, axi.Table("skipped", []string{"what", "line", "text"}, rows))
		return true
	}
	return false
}
