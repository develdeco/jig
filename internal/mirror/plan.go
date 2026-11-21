package mirror

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// PlanComment is one plan comment Sync posted on an issue ticket's issue (or,
// in a dry run, would post).
type PlanComment struct {
	// Ticket is the issue ticket's id.
	Ticket string
	// Tickets is how many of the chart's tickets the comment names.
	Tickets int
}

// planCommentLimit cuts a ticket's description to one short line.
const planCommentLimit = 100

// postPlanComments posts, on the issue of every issue ticket whose chart
// exists and whose issue exists, the chart's plan as a comment, when it
// differs from the last one posted (synced.plan_sha256): a new comment,
// never an edit of an old one. The comment goes first and the hash second, so
// a crash between them can post one duplicate and never loses a comment. skip
// names the items updateIssues skipped on a publish-safety hit: their issues
// get no comment. A comment that trips the publish-safety scan is not posted
// and is a Skipped item, like a body. dryRun reports the comment it would post
// and writes nothing.
func postPlanComments(ctx context.Context, st *store.Store, cfg project.Config, client github.Client, items []item, skip map[string]bool, terms []string, public, dryRun bool, report *SyncReport) error {
	for _, it := range items {
		if it.kind != "ticket" || !it.ref.has() || skip[it.id] {
			continue
		}
		tk, err := st.ReadTicket(it.id)
		if err != nil {
			return err
		}
		if tk.KindOrDefault() != store.KindIssue {
			continue
		}
		chart, err := st.ChartOfIssue(it.id)
		if err != nil {
			return err
		}
		if chart == "" {
			continue
		}
		body, named, err := renderPlanComment(st, cfg, chart)
		if err != nil {
			return err
		}
		rec, err := readRecord(st, it)
		if err != nil {
			return err
		}
		sum := bodyHash(body)
		if rec.Synced != nil && rec.Synced.PlanSHA256 == sum {
			continue
		}
		if dryRun || public {
			if hits := ScanTitleAndBody("", body, terms); len(hits) > 0 {
				report.Skipped = append(report.Skipped, SkippedItem{What: it.id + " plan comment", Line: hits[0].Line, Text: hits[0].Text})
				continue
			}
		}
		report.PlanComments = append(report.PlanComments, PlanComment{Ticket: it.id, Tickets: named})
		if dryRun {
			continue
		}
		if err := client.AddComment(ctx, rec.NodeID, body); err != nil {
			return fmt.Errorf("comment the plan on %s's issue: %w", it.id, err)
		}
		if rec.Synced == nil {
			rec.Synced = &syncedFields{}
		}
		rec.Synced.PlanSHA256 = sum
		if err := writeRecord(st, it, rec); err != nil {
			return err
		}
	}
	return nil
}

// renderPlanComment renders chart's plan: its tickets in the chart's order, a
// multi-task's tasks indented under it, then the chart's footer line. A ticket
// whose issue exists is "- #<n> <title>"; one with none, graduated or not, is
// "- <id or "not graduated"> <title>: <first line of its description>". A line
// ends with "(waits for <ids>)" when the ticket has blocked_by edges. It
// returns the comment and how many tickets it names.
func renderPlanComment(st *store.Store, cfg project.Config, chart string) (string, int, error) {
	entries, err := st.ReadChart(chart)
	if err != nil {
		return "", 0, err
	}
	type row struct {
		entry  store.ChartEntry
		parent int // index into entries, -1 for none
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		rows[i] = row{entry: e, parent: -1}
		under := e.Under
		if under == "" && e.ID != "" {
			if tk, err := st.ReadTicket(e.ID); err == nil {
				under = tk.Parent
			}
		}
		rows[i].parent = indexOfEntry(entries, under)
		if rows[i].parent == i {
			rows[i].parent = -1
		}
	}

	var lines []string
	var emit func(i int, depth int, seen map[int]bool) error
	emit = func(i, depth int, seen map[int]bool) error {
		if seen[i] {
			return nil
		}
		seen[i] = true
		line, err := planLine(st, cfg, entries, rows[i].entry)
		if err != nil {
			return err
		}
		lines = append(lines, strings.Repeat("  ", depth)+line)
		for j := range rows {
			if rows[j].parent == i {
				if err := emit(j, depth+1, seen); err != nil {
					return err
				}
			}
		}
		return nil
	}
	seen := map[int]bool{}
	for i := range rows {
		if rows[i].parent == -1 {
			if err := emit(i, 0, seen); err != nil {
				return "", 0, err
			}
		}
	}
	// An entry in a parent cycle is never reached from the top: list it flat.
	for i := range rows {
		if err := emit(i, 0, seen); err != nil {
			return "", 0, err
		}
	}

	footer := fmt.Sprintf("---\n<sub>Mirrored from the jig ticket store (`charts/%s/`).</sub>", chart)
	return strings.Join(lines, "\n") + "\n\n" + footer, len(lines), nil
}

// indexOfEntry resolves a chart entry reference - "#k" (the 1-based position)
// or a ticket id - to its index in entries, -1 when it names none.
func indexOfEntry(entries []store.ChartEntry, ref string) int {
	if ref == "" {
		return -1
	}
	if k, ok := strings.CutPrefix(ref, "#"); ok {
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(entries) {
			return n - 1
		}
		return -1
	}
	for i, e := range entries {
		if e.ID == ref {
			return i
		}
	}
	return -1
}

// planLine is one ticket's line of the plan comment.
func planLine(st *store.Store, cfg project.Config, entries []store.ChartEntry, e store.ChartEntry) (string, error) {
	title, desc := e.Title, e.Body
	id := "not graduated"
	var waits []string
	graduated := false
	if e.ID != "" {
		if tk, err := st.ReadTicket(e.ID); err == nil {
			graduated = true
			id = e.ID
			title, desc, err = ResolveTicketTitleBody(st, e.ID)
			if err != nil {
				return "", err
			}
			for _, b := range tk.BlockedBy {
				waits = append(waits, b.Ticket)
			}
		}
	}
	if !graduated {
		for _, b := range e.BlockedBy {
			waits = append(waits, entryName(entries, b.Ref))
		}
	}

	var line string
	if number := issueNumberOf(st, cfg, e.ID); graduated && number != "" {
		line = fmt.Sprintf("- #%s %s", number, oneLine(title))
	} else {
		line = fmt.Sprintf("- %s %s", id, oneLine(title))
		if first := firstLine(desc); first != "" {
			line += ": " + first
		}
	}
	if len(waits) > 0 {
		line += " (waits for " + strings.Join(waits, ", ") + ")"
	}
	return line, nil
}

// entryName names the chart entry a blocked_by reference points at: its ticket
// id once graduated, else its title, since a position means nothing to a
// reader of the comment.
func entryName(entries []store.ChartEntry, ref string) string {
	i := indexOfEntry(entries, ref)
	if i < 0 {
		return ref
	}
	if entries[i].ID != "" {
		return entries[i].ID
	}
	return oneLine(entries[i].Title)
}

// issueNumberOf is the number of ticket's issue in the configured repo, "" when
// it has none yet.
func issueNumberOf(st *store.Store, cfg project.Config, ticket string) string {
	if ticket == "" || cfg.GitHub == nil {
		return ""
	}
	ref, err := findRecord(st, store.TrackerOwner{Ticket: ticket}, cfg.GitHub.Repo)
	if err != nil || !ref.inTree {
		return ""
	}
	return ref.entry.ID
}

// oneLine collapses s onto one line.
func oneLine(s string) string {
	return strings.TrimSpace(whitespaceRunRE.ReplaceAllString(s, " "))
}

// firstLine is the first non-empty line of s, cut to planCommentLimit runes.
func firstLine(s string) string {
	for _, l := range strings.Split(trimText(s), "\n") {
		if l = oneLine(l); l != "" {
			return cutRunes(l, planCommentLimit)
		}
	}
	return ""
}
