package land

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/stack"
)

// Judge says whether ticket may land, and what the landing is. It reads, and
// writes nothing: a refusal carries the code and the tickets that must land
// first; otherwise the verdict names the merge to make in each repo that has
// not merged, and the lines to record for a pull request already merged on the
// host. A host jig cannot ask is a note, never a refusal.
func Judge(d Deps, ticket string, opts Options) (Verdict, error) {
	v := Verdict{Ticket: ticket}
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return v, err
	}
	into, err := intoOf(d.Store, ticket, rec, opts.Issue)
	if err != nil {
		return v, err
	}
	v.Into = into
	if v.Landed, err = landedLines(d.Store, ticket, into); err != nil {
		return v, err
	}
	lines, err := journal.ReadFor(d.Store, ticket, into)
	if err != nil {
		return v, err
	}
	published := false
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			published = true
		}
	}
	if rec.Closed != nil && !published {
		return refuse(v, CodeNotPublished, fmt.Sprintf("%s was closed without a pull request: there is nothing to land", ticket), nil,
			fmt.Sprintf("Run `jig status %s`.", ticket)), nil
	}
	if !published {
		return refuse(v, CodeNotPublished, fmt.Sprintf("%s has not published: there is no pull request to land", ticket), nil,
			fmt.Sprintf("Run `jig next %s` for what it needs before it publishes.", ticket)), nil
	}
	own, err := d.Merges.OfFor(ticket, into)
	if err != nil {
		return v, err
	}
	if len(v.Landed) >= len(own.Repos) && len(own.Repos) > 0 && landedAll(own, v.Landed) {
		v.Done = true
		return v, nil
	}
	readPlan := d.Plan
	if readPlan == nil {
		readPlan = func(ticket, issue string) (stack.Plan, error) {
			return stack.ReadFor(stack.Input{Store: d.Store, Cfg: d.Cfg, Clones: d.Clones, Merges: d.Merges}, ticket, issue)
		}
	}
	plan, err := readPlan(ticket, opts.Issue)
	if err != nil {
		return v, err
	}
	for _, r := range plan.Repos {
		if (r.Behind || r.Retarget) && !hasLanding(v.Landed, r.Repo, len(plan.Repos) > 1) {
			return refuse(v, CodeNotPublished, fmt.Sprintf("%s owes a restack or retarget%s: its branch is behind", ticket, inRepoText(r.Repo, len(plan.Repos) > 1)), nil,
				fmt.Sprintf("Merge %s into its branch, then run `jig publish %s`.", r.StartFrom, ticket)), nil
		}
	}
	// What must land first: each rule, in order.
	scene := Scene{Deps: d, Ticket: ticket, Rec: rec, Into: into}
	var first []string
	var firstInto string
	var why []string
	for _, rule := range Rules {
		got, err := rule.Before(scene)
		if err != nil {
			return v, err
		}
		for _, b := range got {
			if !containsString(first, b.Ticket) {
				if len(first) == 0 {
					firstInto = b.Into
				}
				first = append(first, b.Ticket)
				why = append(why, b.Ticket+": "+b.Why)
			}
		}
	}
	if len(first) > 0 {
		return refuse(v, CodeWaits, fmt.Sprintf("%s waits for %s to land first (%s)", ticket, strings.Join(first, ", "), strings.Join(why, "; ")), first,
			fmt.Sprintf("Land %s first: run `%s`.", first[0], Command(d.Store, first[0], firstInto))), nil
	}
	// Each repo not landed: what the host says of its pull request.
	for _, rm := range own.Repos {
		if hasLanding(v.Landed, rm.Repo, len(own.Repos) > 1) {
			continue
		}
		if rm.PR == "" && !rm.Met {
			return refuse(v, CodeNotPublished, fmt.Sprintf("%s has no pull request recorded%s: there is nothing to merge", ticket, inRepoText(rm.Repo, len(own.Repos) > 1)), nil,
				fmt.Sprintf("Run `jig publish %s`.", ticket)), nil
		}
		st, known := repohost.Status{}, false
		if rm.PR != "" && d.Status != nil {
			st, known = d.Status(rm.PR)
		}
		state := rm.State
		if known {
			state = st.State
		} else if rm.PR != "" {
			v.Notes = append(v.Notes, fmt.Sprintf("the host could not be asked about %s: its state is as the merge fact has it", rm.PR))
		}
		base := expectedBase(d, plan, rm.Repo, len(own.Repos) > 1)
		switch {
		case state == ready.PRClosed:
			return refuse(v, CodeClosed, fmt.Sprintf("the pull request %s was closed without merging%s", rm.PR, inRepoText(rm.Repo, len(own.Repos) > 1)), nil,
				fmt.Sprintf("Run `jig publish %s` to open a pull request again.", ticket)), nil
		case state == ready.PRMerged || (rm.PR == "" && rm.Met):
			got := Landing{Repo: rm.Repo, PR: rm.PR, Base: st.Base, Into: into, Commit: st.MergeCommit}
			if got.Base == "" {
				got.Base = base
			}
			v.Record = append(v.Record, got)
		default:
			if known && st.Base != "" && base != "" && st.Base != base {
				return refuse(v, CodeBaseDiffers, fmt.Sprintf("the pull request %s goes into %s, and its plan says %s%s", rm.PR, st.Base, base, inRepoText(rm.Repo, len(own.Repos) > 1)), nil,
					fmt.Sprintf("Run `jig publish %s`: it points the pull request at %s.", ticket, base)), nil
			}
			row := mergeRow(d, rm, st, known, base, ticket, opts.Method)
			if opts.Method != "" && d.Methods != nil {
				if m, ok := d.Methods(rm.PR); ok && !m.Allows(opts.Method) {
					v.Notes = append(v.Notes, "the repo does not allow merging by "+opts.Method+": the host will refuse the command")
				}
			}
			v.Merge = append(v.Merge, row)
		}
	}
	if len(v.Merge) == 0 && len(v.Record) == 0 {
		v.Done = true
	}
	return v, nil
}

// landedAll says every repo of own has a landed line.
func landedAll(own ready.Merge, landed []Landing) bool {
	for _, r := range own.Repos {
		if !hasLanding(landed, r.Repo, len(own.Repos) > 1) {
			return false
		}
	}
	return true
}

// hasLanding says repo has a landed line. A project of one repo names none.
func hasLanding(landed []Landing, repo string, several bool) bool {
	for _, l := range landed {
		if !several || l.Repo == repo {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func inRepoText(repo string, several bool) string {
	if several && repo != "" {
		return " in " + repo
	}
	return ""
}

func refuse(v Verdict, code, msg string, first []string, help ...string) Verdict {
	v.Refusal = &Refusal{Code: code, Msg: msg, First: first, Help: help}
	return v
}

// expectedBase is the branch the pull request of repo goes into by the stack
// plan: its PRTarget, else the repo's target branch.
func expectedBase(d Deps, plan stack.Plan, repo string, several bool) string {
	for _, r := range plan.Repos {
		if (!several || r.Repo == repo) && r.PRTarget != "" {
			return r.PRTarget
		}
	}
	for _, r := range d.Cfg.Repos {
		if !several || r.Name() == repo {
			return r.TargetBranch()
		}
	}
	return "main"
}

// mergeRow names the merge of one pull request: the method the person asked,
// else the one the repo allows (squash, then merge, then rebase), and the host's
// CONFLICTING with what resolves it.
func mergeRow(d Deps, rm ready.RepoMerge, st repohost.Status, known bool, base, ticket, method string) Row {
	row := Row{Repo: rm.Repo, PR: rm.PR, Base: base}
	if known && st.Base != "" {
		row.Base = st.Base
	}
	row.Method = method
	if row.Method == "" {
		row.Method = "squash"
		if d.Methods != nil {
			if m, ok := d.Methods(rm.PR); ok && m.Preferred() != "" {
				row.Method = m.Preferred()
			}
		}
	}
	row.Command = "gh pr merge " + rm.PR + " --" + row.Method
	if known && st.Mergeable == repohost.MergeableConflict {
		row.Conflicting = true
		row.Resolve = fmt.Sprintf("the host reports it CONFLICTING: in the ticket's worktree run `git merge origin/%s`, resolve it, then `jig publish %s` (a merge resolved by hand is a delta for review)", row.Base, ticket)
	}
	return row
}
