package phase

import "github.com/develdeco/jig/internal/journal"

// Work is the phases of the ticket's work, brief to publish: what the TUI draws
// and what a ticket's steps are. Land follows them and is not one of its steps.
func (t Ticket) Work() []Phase { return t.Phases[:Work] }

// MergedOnHost says the ticket published, has no landed line and the host
// reports its pull requests merged: a merge made on the host and not recorded.
// `jig land` records it.
func (t Ticket) MergedOnHost() bool { return t.d != nil && t.d.mergedOnHost() }

// LandedIn reads a ticket's journal lines for its landing: hasPR says some
// repo has a recorded pull request, and landed that every pull request has a
// landed line for its repo (and, in a shared task, its issue). A landed line
// with no `into` stands for a pull request of any, and so does a pull request
// with none, as a journal written before a ticket could be shared reads.
func LandedIn(lines []journal.Line) (landed, hasPR bool) {
	type key struct{ repo, into string }
	var prs []key
	seen := map[key]bool{}
	var landings []key
	for _, l := range lines {
		switch l.Event {
		case journal.EventPr:
			if l.URL == "" {
				continue
			}
			if k := (key{l.Repo, l.Into}); !seen[k] {
				seen[k] = true
				prs = append(prs, k)
			}
		case journal.EventLanded:
			landings = append(landings, key{l.Repo, l.Into})
		}
	}
	if len(prs) == 0 {
		return false, false
	}
	for _, p := range prs {
		found := false
		for _, l := range landings {
			if l.repo == p.repo && (l.into == p.into || l.into == "" || p.into == "") {
				found = true
				break
			}
		}
		if !found {
			return false, true
		}
	}
	return true, true
}

func (d *digest) mergedOnHost() bool {
	return d.f.Published && d.f.Closed == "" && d.hasPR && !d.landed && d.f.MergedOnHost
}

// land derives the Land phase: done when every pull request the ticket
// published has a landed line, current once the publish is done and it has not,
// with the words `waits for a merge` or, for a merge the host reports and
// nobody recorded, `merged on the host, not recorded`. A ticket closed without
// a pull request has no Land, and neither has one that published none (a repo
// with no pull request host): both are done.
func (d *digest) land(publishDone bool) (Phase, bool) {
	p := Phase{Name: Land}
	d.landed, d.hasPR = LandedIn(d.lines)
	d.landed = d.landed || d.f.Landed
	merge := Sub{Key: "merge", Label: "merge", State: SubPending}
	record := Sub{Key: "record", Label: "record", State: SubPending}
	switch {
	case d.f.Closed != "":
		p.Text = "none · closed without a pull request"
		return p, true
	case d.landed:
		p.Text = "landed"
		merge.State, record.State = SubDone, SubDone
	case !d.hasPR && publishDone:
		p.Text = "none · no pull request to merge"
		return p, true
	case !publishDone:
		p.Text = "after the publish"
	case d.f.MergedOnHost:
		p.Text = "merged on the host, not recorded"
		merge.State, record.State = SubDone, SubActive
		record.Detail = "`jig land " + d.f.Ticket + "` records it"
	default:
		p.Text = "waits for a merge"
		merge.State = SubWaiting
		merge.Detail = "the session merges it at the person's word"
	}
	p.Subs = []Sub{merge, record}
	return p, d.landed && publishDone
}
