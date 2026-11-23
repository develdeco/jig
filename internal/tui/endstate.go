package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// A ticket that stands at its end reads as exactly one of four states, each
// with its own word and icon: published (its pull request is open and its
// landing cannot be asked for yet), waits to land (the person's word is the
// next move), landed, and closed. endStateOf says which, from the ticket's
// digest and the landed lines of its journal alone: never from a host poll made
// at draw time, and never from a phase being done, so the header, the project's
// list, the chart's row and Detail and the Steps panel's last rows say the
// same one.
const (
	// StateOnLand is a ticket whose pull request is open behind tickets that
	// must land first.
	StateOnLand = "on-land"
	// StateWaitsToLand is a ticket whose landing waits for the person's word,
	// or whose merge the host made and nobody recorded.
	StateWaitsToLand = "waits-to-land"
	// StateLanded is a ticket every pull request of which landed.
	StateLanded = "landed"
	// StateClosed is a ticket closed without a pull request to merge.
	StateClosed = "closed"
)

// endState is where a ticket stands at its end.
type endState struct {
	// State is the state the screens draw it in: its icon and colour.
	State string
	// Word is the state in a few words, for a header: published, waits to land,
	// landed or closed.
	Word string
	// Words is the whole sentence, for a list's row.
	Words string
}

// landedAt is one landed line of a ticket: the repo, the branch it merged into,
// when the line was written (as the screens show the time) and who recorded it
// (its provenance, and the session that wrote it).
type landedAt struct {
	Repo, Into, At, Provenance, Session string
	// Container is the issue or multi-task the landing was into, by id, when the
	// line names one: the one thing that tells the landings of a task two issues
	// list apart.
	Container string
}

// endStateOf is the end state of ticket id as its digest says it stands and its
// landed lines say it landed; false for a ticket that has not reached its end:
// not published yet, built again after its publish, or behind.
func endStateOf(id string, d *digestInfo, landed []landedAt) (endState, bool) {
	if d == nil {
		return endState{}, false
	}
	if d.Closed != "" {
		return endState{StateClosed, "closed", "closed · " + d.Closed}, true
	}
	publish, ok := d.phase(phase.Publish)
	if !ok || publish.State != phase.StateDone {
		return endState{}, false
	}
	land, ok := d.phase(phase.Land)
	if !ok {
		return endState{}, false
	}
	if land.State == phase.StateDone {
		return endState{StateLanded, "landed", landedWords(land.Text, landed)}, true
	}
	say := `say "Land ` + id + `"`
	switch {
	case strings.HasPrefix(land.Text, "merged on the host"):
		return endState{StateWaitsToLand, "waits to land", land.Text + " · " + say}, true
	case len(d.LandFirst) > 0:
		return endState{StateOnLand, "published", "published · " + prWordsWithState(d.PRs) + " · lands after " + strings.Join(d.LandFirst, ", ")}, true
	}
	return endState{StateWaitsToLand, "waits to land", "waits to land · " + say}, true
}

// landedWords is a landed ticket in words: where each pull request went and
// when its landing was recorded; the Land phase's own words (a ticket with
// nothing to merge) when no line says.
func landedWords(own string, landed []landedAt) string {
	if len(landed) == 0 {
		return own
	}
	several := false
	for _, l := range landed[1:] {
		several = several || l.Repo != landed[0].Repo
	}
	parts := make([]string, len(landed))
	for i, l := range landed {
		part := "into " + firstOf(l.Into, "its target")
		if several && l.Repo != "" {
			part = l.Repo + " " + part
		}
		if l.At != "" {
			part += " at " + l.At
		}
		parts[i] = part
	}
	return "landed · " + strings.Join(parts, "; ")
}

// prWordsWithState are the pull requests as prWords says them with the host's
// word on each: open, merged or closed, and "state not read" for one the digest
// has no fact for, which is never taken as open.
func prWordsWithState(prs []digestPR) string {
	out := make([]string, len(prs))
	for i, pr := range prs {
		out[i] = prWords([]digestPR{pr}, true)
	}
	return strings.Join(out, ", ")
}

// prWords are the pull requests by number and target ("PR #503 into
// 501-review-rounds-stop-repeating-work"), the URL of one with no number, "" for
// none. state adds the host's word on each: "PR #503 open into main".
func prWords(prs []digestPR, state bool) string {
	var out []string
	for _, pr := range prs {
		word, note := "", ""
		if state {
			switch s := strings.ToLower(pr.State); s {
			case "open", "merged", "closed":
				word = " " + s
			default:
				note = " (state not read)"
			}
		}
		switch {
		case pr.Number > 0 && pr.Target != "":
			out = append(out, "PR #"+strconv.Itoa(pr.Number)+word+" into "+pr.Target+note)
		case pr.Number > 0:
			out = append(out, "PR #"+strconv.Itoa(pr.Number)+word+note)
		default:
			out = append(out, pr.URL+word+note)
		}
	}
	return strings.Join(out, ", ")
}

// landedLines are the landed lines of the ticket, the latest of each repo and
// container, oldest first, with the time of each as the screens show it.
func (m *Model) landedLines(t *ticket) []landedAt {
	type key struct{ repo, into string }
	var order []key
	latest := map[key]landedAt{}
	for _, l := range t.lines {
		if l.Event != journal.EventLanded {
			continue
		}
		k := key{l.Repo, l.Into}
		if _, ok := latest[k]; !ok {
			order = append(order, k)
		}
		latest[k] = landedAt{Repo: l.Repo, Into: firstOf(l.Base, l.Into), At: clock(l, m.zone), Provenance: l.Provenance, Session: l.Session, Container: l.Into}
	}
	out := make([]landedAt, len(order))
	for i, k := range order {
		out[i] = latest[k]
	}
	return out
}

// endOf is the end state of the ticket, false until it stands at one.
func (m *Model) endOf(t *ticket) (endState, bool) {
	if t == nil {
		return endState{}, false
	}
	return endStateOf(t.id, t.digest, m.landedLines(t))
}
