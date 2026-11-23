package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// nextSuiteRow is one row of an issue's suite history in a repo: where the
// suite stands at the tip of the issue's branch (land.SuiteOf) or, for a row of
// an earlier tip, the failed final run that was made there. Fixes are the tasks
// of the issue begun after that failed run, which is how the plan marks a fix
// task: the chart carries no mark of its own.
type nextSuiteRow struct {
	Repo      string   `json:"repo"`
	Standing  string   `json:"standing"`
	Command   string   `json:"command,omitempty"`
	Tip       string   `json:"tip,omitempty"`
	Seconds   int      `json:"seconds,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Waiver    string   `json:"waiver,omitempty"`
	Fixes     []string `json:"fixes,omitempty"`
	// Why is the sentence of a suite that could not be read or has no run.
	Why string `json:"why,omitempty"`
}

// withPlanFacts adds to n, the status of an issue or a multi-task, what the
// plan screen reads and the roster does not: the ticket's body whole and, for
// an issue, its suite rows. A multi-task has no suite.
func (n nextTicket) withPlanFacts(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, data ticketData) (nextTicket, error) {
	n.Need = strings.TrimSpace(data.rec.Body)
	// Its own standing is the one `jig next` derives, read by the same function,
	// so the plan's pull-request line and prompt say what `jig next` says.
	if merges != nil {
		if r, err := containerReadiness(st, cfg, mp, merges, n.Ticket); err == nil {
			n.Standing, n.Prompt = r.State, r.Prompt
			for _, w := range r.Waits {
				n.Waits = append(n.Waits, nextWait{Ticket: w.Ticket, Standing: w.Standing})
			}
			for _, u := range r.Unmet {
				n.Unmet = append(n.Unmet, nextUnmet{Code: u.Code, Why: u.Why, Prompt: u.Prompt})
			}
			n.Waived = r.Waived
		}
	}
	// Its branch is a name the store makes, so the plan of a multi-task, which
	// has no chart verb of its own, reads it here.
	if branch, err := ticketBranch(st, cfg, n.Ticket, data.rec); err == nil {
		n.Branch = branch
	}
	if n.Kind != store.KindIssue {
		return n, nil
	}
	rows, err := suiteRows(st, cfg, mp, merges, n.Ticket, data.rec)
	if err != nil {
		// A suite that cannot be read is a row that says why, not a refusal of
		// the whole status: the kind, chart and children stand.
		rows = nil
		for _, r := range cfg.Repos {
			rows = append(rows, nextSuiteRow{Repo: r.Name(), Standing: land.SuiteUnrun, Why: err.Error()})
		}
		if len(rows) == 0 {
			rows = []nextSuiteRow{{Standing: land.SuiteUnrun, Why: err.Error()}}
		}
	}
	n.Suite = rows
	return n, nil
}

// suiteRows are the suite rows of issue id: for each repo it builds in, one row
// for every earlier tip whose latest final run failed, oldest first, then the
// row of the issue's own standing. Only a pushed issue at its suite stage reads
// land.SuiteOf at its tip; a landed issue reads what was recorded, a repo that
// declares no suite reads none recorded at every stage, and an issue whose tasks
// have not landed or whose branch is not pushed says what it waits for. merges
// nil asks nothing of the stage.
func suiteRows(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, id string, rec store.Ticket) ([]nextSuiteRow, error) {
	d := land.Deps{Store: st, Cfg: cfg, Clones: mp.Clones}
	landed, waiting := false, false
	if merges != nil {
		if r, err := containerReadiness(st, cfg, mp, merges, id); err == nil {
			landed, waiting = r.State == land.StateLanded, len(r.Waits) > 0
		}
	}
	c, err := land.ContainerOf(d, id)
	if err != nil {
		return nil, err
	}
	suites, err := land.SuiteOf(d, c)
	if err != nil {
		return nil, err
	}
	lines, err := journal.Read(st, id)
	if err != nil {
		return nil, err
	}
	begun := tasksBegun(st, land.Children(st, id, rec))
	var out []nextSuiteRow
	for _, s := range suites {
		runs := finalRunsOf(lines, land.JournalRepo(d, s.Repo))
		if landed {
			// A landed issue reads what was recorded, whatever its branch has become.
			row := landedSuiteRow(s.Repo, runs, lines, land.JournalRepo(d, s.Repo))
			for _, r := range earlierFailures(runs, row.Tip) {
				out = append(out, nextSuiteRow{
					Repo: s.Repo, Standing: land.SuiteFail, Command: r.line.Command, Tip: r.line.Commit,
					Seconds: r.line.Seconds, Signature: r.line.Signature, Fixes: fixesAfter(begun, runs, r),
				})
			}
			out = append(out, row)
			continue
		}
		for _, r := range earlierFailures(runs, s.Tip) {
			out = append(out, nextSuiteRow{
				Repo: s.Repo, Standing: land.SuiteFail, Command: r.line.Command, Tip: r.line.Commit,
				Seconds: r.line.Seconds, Signature: r.line.Signature, Fixes: fixesAfter(begun, runs, r),
			})
		}
		if waiting && s.Standing == land.SuiteUnrun && mp.Clones[s.Repo] != "" {
			// Before every task has landed `jig next` says the issue waits for them,
			// and a repo that declares no suite has none to wait for. Once they
			// have, the standing is SuiteOf's, as `jig next` words it.
			if undeclaredAtTarget(cfg, mp.Clones[s.Repo], s.Repo) {
				out = append(out, nextSuiteRow{Repo: s.Repo, Standing: land.SuiteUndeclared})
			} else {
				out = append(out, nextSuiteRow{Repo: s.Repo, Standing: land.SuiteUnrun, Command: s.Command, Why: "waits for its tasks to land before the suite is run"})
			}
			continue
		}
		row := nextSuiteRow{Repo: s.Repo, Standing: s.Standing, Command: s.Command, Tip: s.Tip}
		switch s.Standing {
		case land.SuitePass, land.SuiteFail:
			row.Seconds, row.Signature = s.Run.Seconds, s.Run.Signature
		case land.SuiteWaived:
			row.Waiver = s.Waiver.Summary
		case land.SuiteUnrun:
			row.Why = s.Why
		}
		if s.Standing == land.SuiteFail {
			for _, r := range runs {
				if r.line.Commit == s.Tip {
					row.Fixes = fixesAfter(begun, runs, r)
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// landedSuiteRow is a landed issue's suite as it was recorded: the last final run
// that judged the code, which a waiver at its tip stands over as it did before
// the landing, else the last waiver, else none recorded.
func landedSuiteRow(repo string, runs []finalRun, lines []journal.Line, jr string) nextSuiteRow {
	if len(runs) > 0 {
		r := runs[len(runs)-1].line
		if r.Outcome != "pass" {
			if w, ok := journal.SuiteWaivedAt(lines, jr, r.Commit); ok {
				return nextSuiteRow{Repo: repo, Standing: land.SuiteWaived, Tip: w.Commit, Waiver: w.Summary}
			}
		}
		return nextSuiteRow{Repo: repo, Standing: map[bool]string{true: land.SuitePass, false: land.SuiteFail}[r.Outcome == "pass"], Command: r.Command, Tip: r.Commit, Seconds: r.Seconds, Signature: r.Signature}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := lines[i]; l.Event == journal.EventSuiteWaived && l.Repo == jr {
			return nextSuiteRow{Repo: repo, Standing: land.SuiteWaived, Tip: l.Commit, Waiver: l.Summary}
		}
	}
	return nextSuiteRow{Repo: repo, Standing: land.SuiteUndeclared}
}

// undeclaredAtTarget says the repo's manifest at its target branch declares no
// suite: a repo that declares none has none to wait for at any stage.
func undeclaredAtTarget(cfg project.Config, dir, repo string) bool {
	for _, r := range cfg.Repos {
		if r.Name() != repo {
			continue
		}
		tip, err := gitx.RevParse(dir, "origin/"+r.TargetBranch())
		if err != nil || tip == "" {
			return false
		}
		m, err := manifest.ResolveAt(dir, tip)
		if err != nil {
			return false
		}
		_, ok := m.Oracles["suite"]
		return !ok
	}
	return false
}

// finalRun is a final run that judged the code, with when it was made.
type finalRun struct {
	line journal.Line
	at   time.Time
}

// finalRunsOf are the final runs of one repo that covered their commit and
// judged the code (a pass or a fail that no stop ended), in journal order.
func finalRunsOf(lines []journal.Line, repo string) []finalRun {
	var out []finalRun
	for _, l := range lines {
		if l.Event != journal.EventFinalOracle || l.Repo != repo || l.Commit == "" || l.Uncovered != "" || l.Cause == journal.CauseStopped {
			continue
		}
		if l.Outcome != "pass" && l.Outcome != "fail" {
			continue
		}
		out = append(out, finalRun{line: l, at: parseTS(l.TS)})
	}
	return out
}

// earlierFailures are the runs that stand for a tip no longer the branch's: the
// latest run at each such tip, when it failed, oldest first.
func earlierFailures(runs []finalRun, tip string) []finalRun {
	var out []finalRun
	for i, r := range runs {
		if r.line.Commit == tip || r.line.Outcome != "fail" {
			continue
		}
		latest := true
		for _, later := range runs[i+1:] {
			if later.line.Commit == r.line.Commit {
				latest = false
			}
		}
		if latest {
			out = append(out, r)
		}
	}
	return out
}

// taskStart is when a task began: the time of its first journal line, zero for
// a task with none.
type taskStart struct {
	id    string
	start time.Time
}

// tasksBegun reads when each open task of an issue began. A closed task is no
// fix of anything.
func tasksBegun(st *store.Store, ids []string) []taskStart {
	var out []taskStart
	for _, id := range ids {
		if rec, err := st.ReadTicket(id); err != nil || rec.Closed != nil {
			continue
		}
		t := taskStart{id: id}
		if lines, err := journal.Read(st, id); err == nil && len(lines) > 0 {
			t.start = parseTS(lines[0].TS)
		}
		out = append(out, t)
	}
	return out
}

// fixesAfter are the tasks begun after the failed run r and before the next run
// of the repo, which was made on what they added. A task with no journal line
// has begun nothing: it is newer than every run, so it is a fix of the last
// run, when that one failed.
func fixesAfter(tasks []taskStart, runs []finalRun, r finalRun) []string {
	var next *finalRun
	for i := range runs {
		if runs[i].line.Commit == r.line.Commit {
			next = nil
			if i+1 < len(runs) {
				next = &runs[i+1]
			}
		}
	}
	var out []string
	for _, t := range tasks {
		switch {
		case t.start.IsZero():
			if next == nil {
				out = append(out, t.id)
			}
		case t.start.After(r.at) && (next == nil || t.start.Before(next.at)):
			out = append(out, t.id)
		}
	}
	return out
}

// parseTS reads a journal line's time, zero when it has none.
func parseTS(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// suiteBlock is the suite table `jig status <issue>` prints.
func suiteBlock(rows []nextSuiteRow) string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		secs := "-"
		if r.Standing == land.SuitePass || r.Standing == land.SuiteFail {
			secs = strconv.Itoa(r.Seconds)
		}
		table[i] = []string{r.Repo, r.Standing, dash(r.Command), dash(r.Tip), secs, dash(r.Signature), dash(r.Waiver), dash(strings.Join(r.Fixes, "+")), dash(r.Why)}
	}
	return axi.Table("suite", []string{"repo", "standing", "command", "tip", "seconds", "signature", "waiver", "fixes", "why"}, table)
}
