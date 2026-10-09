package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// ticketTitle is a new issue's title for ticket: its ticket.yaml title when
// one is recorded, else the ticket's own id. The fuller fallback chain
// (ResolveTicketTitleBody) is what the renderer uses once an issue exists to
// render (or re-render) its title and body; this one stays narrow because
// Sync only ever needs a title the moment it creates an issue, when no body
// citing it needs qualifying yet.
func ticketTitle(st *store.Store, ticket string) (string, error) {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return "", err
	}
	if rec.Title != "" {
		return rec.Title, nil
	}
	return ticket, nil
}

// chartTitle is a chart's title (brief.md#What an issue shows): the first
// line of its map.md when that line is a "# " heading, else "Chart:
// <name>". No title carries an id.
func chartTitle(st *store.Store, chart string) (string, error) {
	data, err := os.ReadFile(st.ChartMapFile(chart))
	if err != nil {
		if os.IsNotExist(err) {
			return "Chart: " + chart, nil
		}
		return "", err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	first = strings.TrimRight(first, "\r")
	if strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(strings.TrimPrefix(first, "# ")), nil
	}
	return "Chart: " + chart, nil
}

// footer is the body every issue ends with (brief.md#What an issue shows).
// where is the ticket id or "charts/<name>/"; marker is
// "<!-- jig:<id> -->" or "<!-- jig:chart:<name> -->", with no trailing
// newline.
func footer(where, marker string) string {
	return fmt.Sprintf(
		"---\n<sub>Mirrored from the jig ticket store (`%s`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n%s",
		where, marker,
	)
}

// ticketFooter is footer for ticket.
func ticketFooter(ticket string) string {
	return footer(ticket, fmt.Sprintf("<!-- jig:%s -->", ticket))
}

// chartFooter is footer for chart.
func chartFooter(chart string) string {
	where := "charts/" + chart + "/"
	return footer(where, fmt.Sprintf("<!-- jig:chart:%s -->", chart))
}

// ResolveTicketTitleBody resolves ticket's title and its description
// separately, each from the first of these that has it
// (brief.md#What an issue shows):
//  1. its ticket.yaml (title:, body:);
//  2. its entry in a chart's tickets.yaml (title:, body:);
//  3. the ticket's id, as the title, with no description.
//
// A fourth rank once read the latest committed <ticket>/tracker/ticket.md
// (a "# " first line as the title, the rest as the description) whose
// commit was not publish's own: a v1 store, before every ticket's title and
// description lived in ticket.yaml. `jig store migrate` carries that step
// forward one last time, on the v1 store it reads before deleting
// tracker/ticket.md (brief.md#The migration), and this function no longer
// needs it - a schema-2 store, the only kind any command but `jig store
// migrate` ever opens, has already had it rewritten into ticket.yaml.
func ResolveTicketTitleBody(st *store.Store, ticket string) (title, body string, err error) {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return "", "", err
	}
	title, body = rec.Title, rec.Body

	if title == "" || body == "" {
		entry, found, err := findChartEntry(st, ticket)
		if err != nil {
			return "", "", err
		}
		if found {
			if title == "" {
				title = entry.Title
			}
			if body == "" {
				body = entry.Body
			}
		}
	}

	if title == "" {
		title = ticket
	}
	return title, body, nil
}

// findChartEntry looks for ticket among every chart's tickets.yaml, in
// chart name order, returning the first match.
func findChartEntry(st *store.Store, ticket string) (store.ChartEntry, bool, error) {
	names, err := st.ChartNames()
	if err != nil {
		return store.ChartEntry{}, false, err
	}
	for _, name := range names {
		entries, err := st.ReadChart(name)
		if err != nil {
			return store.ChartEntry{}, false, err
		}
		for _, e := range entries {
			if e.ID == ticket {
				return e, true, nil
			}
		}
	}
	return store.ChartEntry{}, false, nil
}

// waitsForLine renders "**Waits for:** ..." for blockedBy, or "" when there
// is none (brief.md#What an issue shows): for each blocked_by edge in
// order, "<id> (#<number>, <kind>)", or "<id> (<kind>)" when the blocker
// has no issue, joined by ", ".
func waitsForLine(st *store.Store, blockedBy []store.TicketBlockedBy) (string, error) {
	if len(blockedBy) == 0 {
		return "", nil
	}
	parts := make([]string, len(blockedBy))
	for i, b := range blockedBy {
		blockerID, err := st.ResolveTicket(b.Ticket)
		if err != nil {
			return "", err
		}
		_, abs := ticketRecordPath(st, blockerID)
		has, err := hasRecord(abs)
		if err != nil {
			return "", err
		}
		if has {
			rec, err := readRecord(abs)
			if err != nil {
				return "", err
			}
			parts[i] = fmt.Sprintf("%s (#%d, %s)", blockerID, rec.Issue, b.Kind)
		} else {
			parts[i] = fmt.Sprintf("%s (%s)", blockerID, b.Kind)
		}
	}
	return "**Waits for:** " + strings.Join(parts, ", "), nil
}

// whitespaceRunRE matches a run of whitespace (including newlines), for
// collapseGoal.
var whitespaceRunRE = regexp.MustCompile(`\s+`)

// collapseGoal collapses goal onto one line and cuts it to 200 runes (199
// plus "…" when longer), per brief.md#What an issue shows's slices line.
func collapseGoal(goal string) string {
	g := strings.TrimSpace(whitespaceRunRE.ReplaceAllString(goal, " "))
	return cutRunes(g, 200)
}

// cutRunes returns s unchanged when it has at most max runes, else its
// first max-1 runes followed by "…".
func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// renderSlicesBlock renders "## Slices" and one line per slice.yaml
// entry, or "" when the ticket has none.
func renderSlicesBlock(st *store.Store, ticket string) (string, error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	if len(slices) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(slices))
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return "", err
		}
		box := "- [ ] "
		if ss.State == "green" {
			box = "- [x] "
		}
		line := fmt.Sprintf("%s`%s` %s", box, sl.ID, collapseGoal(sl.Goal))
		if ss.State != "green" && ss.State != "queued" {
			line += fmt.Sprintf(" *(%s)*", ss.State)
		}
		lines = append(lines, line)
	}
	return "## Slices\n\n" + strings.Join(lines, "\n"), nil
}

// renderPRBlock renders "## Pull requests" and one line per pr, or "" when
// there are none.
func renderPRBlock(prs []PRRef) string {
	if len(prs) == 0 {
		return ""
	}
	lines := make([]string, len(prs))
	for i, pr := range prs {
		lines[i] = fmt.Sprintf("- %s/%s#%d (%s)", pr.Owner, pr.Repo, pr.Number, strings.ToLower(pr.State))
	}
	return "## Pull requests\n\n" + strings.Join(lines, "\n")
}

// renderBriefBlock renders the <details> brief block, unwrapped, qualified
// and truncated to GitHub's cap, or "" when the ticket has no brief.md.
func renderBriefBlock(st *store.Store, ticket string, needQualify bool, ownerRepo string) (string, error) {
	data, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	text := qualify(unwrap(trimText(string(data))), needQualify, ownerRepo)
	text = truncateSection(text, "the full brief is in the store.")
	return "<details><summary>Brief</summary>\n\n" + text + "\n\n</details>", nil
}

// RenderTicketBody renders ticket's full issue body (brief.md#What an issue
// shows): its description, "Waits for", "## Slices", "## Pull requests",
// the brief, and the footer, each part left out when empty, joined by one
// blank line.
func RenderTicketBody(st *store.Store, cfg project.Config, ticket string, prs []PRRef) (string, error) {
	_, desc, err := ResolveTicketTitleBody(st, ticket)
	if err != nil {
		return "", err
	}
	ownerRepo, needQualify := qualifyRepo(cfg)

	var parts []string
	if desc != "" {
		parts = append(parts, qualify(unwrap(trimText(desc)), needQualify, ownerRepo))
	}

	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return "", err
	}
	waits, err := waitsForLine(st, rec.BlockedBy)
	if err != nil {
		return "", err
	}
	if waits != "" {
		parts = append(parts, waits)
	}

	slicesBlock, err := renderSlicesBlock(st, ticket)
	if err != nil {
		return "", err
	}
	if slicesBlock != "" {
		parts = append(parts, slicesBlock)
	}

	if prBlock := renderPRBlock(prs); prBlock != "" {
		parts = append(parts, prBlock)
	}

	briefBlock, err := renderBriefBlock(st, ticket, needQualify, ownerRepo)
	if err != nil {
		return "", err
	}
	if briefBlock != "" {
		parts = append(parts, briefBlock)
	}

	parts = append(parts, ticketFooter(ticket))
	return strings.Join(parts, "\n\n"), nil
}

// RenderChartBody renders chart's full issue body (brief.md#What an issue
// shows): its map.md without the heading line, unwrapped and qualified, a
// blank line, then the footer.
func RenderChartBody(st *store.Store, cfg project.Config, chart string) (string, error) {
	data, err := os.ReadFile(st.ChartMapFile(chart))
	if err != nil {
		if os.IsNotExist(err) {
			return chartFooter(chart), nil
		}
		return "", err
	}
	text := stripHeadingLine(trimText(string(data)))
	ownerRepo, needQualify := qualifyRepo(cfg)
	text = qualify(unwrap(text), needQualify, ownerRepo)
	text = truncateSection(text, "the full map is in the store.")
	if text == "" {
		return chartFooter(chart), nil
	}
	return text + "\n\n" + chartFooter(chart), nil
}

// trimText normalizes line endings to "\n" and trims surrounding
// whitespace, the shape every text block (description, brief, map) is
// unwrapped and qualified from.
func trimText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// stripHeadingLine drops text's first line (a chart's map.md heading) and
// any blank line right after it.
func stripHeadingLine(text string) string {
	_, rest, found := strings.Cut(text, "\n")
	if !found {
		return ""
	}
	return strings.TrimLeft(rest, "\n")
}

// qualifyRepo reports the product repo's "owner/repo" and whether bare
// "#123" references need qualifying with it (brief.md#What an issue
// shows): the project has exactly one repo, its GitHub owner/repo is known,
// and it differs from the issue repo.
func qualifyRepo(cfg project.Config) (ownerRepo string, needed bool) {
	if cfg.GitHub == nil || len(cfg.Repos) != 1 {
		return "", false
	}
	owner, repo, ok := repohost.OwnerRepo(cfg.Repos[0].Remote)
	if !ok {
		return "", false
	}
	ownerRepo = owner + "/" + repo
	if strings.EqualFold(ownerRepo, cfg.GitHub.Repo) {
		return "", false
	}
	return ownerRepo, true
}

// codeSpanOrFenceRE matches a fenced code block or an inline code span, the
// stretches of text qualify leaves untouched.
var codeSpanOrFenceRE = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\n]*`")

// bareIssueRefRE matches a bare "#123" not already qualified (not preceded
// by a word character or "/", which would make it part of "owner/repo#123"
// or "word#123" already).
var bareIssueRefRE = regexp.MustCompile(`(^|[^\w/])#(\d+)`)

// qualify rewrites every bare "#123" outside code spans and fenced blocks
// to "<ownerRepo>#123", when needed (brief.md#What an issue shows).
func qualify(text string, needed bool, ownerRepo string) string {
	if !needed {
		return text
	}
	var out strings.Builder
	rest := text
	for {
		loc := codeSpanOrFenceRE.FindStringIndex(rest)
		if loc == nil {
			out.WriteString(qualifyPlain(rest, ownerRepo))
			break
		}
		out.WriteString(qualifyPlain(rest[:loc[0]], ownerRepo))
		out.WriteString(rest[loc[0]:loc[1]])
		rest = rest[loc[1]:]
	}
	return out.String()
}

func qualifyPlain(s, ownerRepo string) string {
	return bareIssueRefRE.ReplaceAllString(s, "${1}"+ownerRepo+"#${2}")
}

// fenceLineRE, blockStartRE and listItemLineRE recognize the line shapes
// the bridge's unwrapping rule treats specially (brief.md#What an issue
// shows's unwrapping rule), all at any indent: blockStartRE is a list
// item, a heading, a table row, a quote, raw HTML or a rule.
var (
	fenceLineRE    = regexp.MustCompile("^\\s*(```|~~~)")
	blockStartRE   = regexp.MustCompile(`^\s*(([-*+]|\d+[.)])\s|#{1,6}\s|\||>|<|(-{3,}|\*{3,}|_{3,})\s*$)`)
	listItemLineRE = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
)

// unwrap joins the hard-wrapped lines of each paragraph and list item
// (brief.md#What an issue shows's unwrapping rule, the bridge's own).
// GitHub renders every newline in an issue body as a line break, so store
// text wrapped at 72 columns would break mid-sentence. Fenced and indented
// code, headings, tables, quotes, HTML and rules keep their lines.
func unwrap(md string) string {
	var out []string
	inFence, verbatim, joinable := false, false, false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fenceLineRE.MatchString(line):
			inFence = !inFence
			out, joinable = append(out, line), false
		case inFence:
			out = append(out, line)
		case trimmed == "":
			out, joinable, verbatim = append(out, line), false, false
		case verbatim:
			out = append(out, line)
		case blockStartRE.MatchString(line) || !joinable:
			// An indented block after a blank line that is not a list item
			// is code: keep its lines as they are.
			verbatim = !joinable && len(line)-len(strings.TrimLeft(line, " ")) >= 4 && !listItemLineRE.MatchString(line) && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == ""
			out = append(out, line)
			joinable = !verbatim && (listItemLineRE.MatchString(line) || !blockStartRE.MatchString(line))
		default:
			prev := out[len(out)-1]
			if strings.HasSuffix(prev, "  ") || strings.HasSuffix(prev, `\`) {
				out = append(out, line) // an explicit line break
				continue
			}
			out[len(out)-1] = prev + " " + trimmed
		}
	}
	return strings.Join(out, "\n")
}

// issueBodyByteCap is the byte budget truncateSection keeps a rendered
// section within, short of GitHub's own 65536-character cap
// (brief.md#What an issue shows).
const issueBodyByteCap = 60000

// truncateSection cuts section on a rune boundary so it (plus the
// truncation note) stays within issueBodyByteCap, appending a blank line
// and "*(Truncated: <note>)*" when it had to cut.
func truncateSection(section, note string) string {
	noteBlock := "\n\n*(Truncated: " + note + ")*"
	budget := issueBodyByteCap - len(noteBlock)
	if len(section) <= budget {
		return section
	}
	if budget < 0 {
		budget = 0
	}
	cut := []rune(section)
	for len(string(cut)) > budget {
		cut = cut[:len(cut)-1]
	}
	return string(cut) + noteBlock
}
