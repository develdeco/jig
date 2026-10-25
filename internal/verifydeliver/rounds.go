package verifydeliver

// The gate rounds a ticket has recorded, read back for display. A round leaves
// a report.yaml (its verdict, reviewer and reviewed head) and, when a reviewer
// ran it, a findings.yaml (its scope, the paths it read and what it found).
// ReadRounds joins the two, and the journal's gate lines for a round that
// carries no reviewer of its own, so `jig status` and the screens show a round
// from one reading.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// The verdicts a RoundRecord reports. A round that is not clean is "findings"
// whether the findings were fixes, asks or notes; VerdictNotIndependent is its
// own, since such a round raised nothing a person can act on but its reviewer.
const (
	RoundClean    = "clean"
	RoundFindings = "findings"
)

// RoundRecord is one recorded gate round.
type RoundRecord struct {
	Round int
	// Verdict is RoundClean, RoundFindings or VerdictNotIndependent.
	Verdict string
	// Reviewer is who reported the round: the report's own record of them, else
	// the session the journal's gate lines name. The zero value is a round no
	// reviewer ran (a scripted one).
	Reviewer Provenance
	// Model and Effort are what the reviewer said it ran on, "" when it said
	// nothing. Harness is the harness the journal says the reviewer's session
	// runs in, "" when no line of that session names one (the machine ledger,
	// which the store does not hold, knows it better).
	Model, Effort, Harness string
	// Independent says whether the reviewer was independent of the builders:
	// "" for a round no reviewer ran.
	Independent Independence
	// Scope is full or delta, "" for a round no reviewer ran.
	Scope string
	// Base and Head are the range the round reviewed, as full shas: Base is ""
	// when it is not known (a round whose request is gone and whose scope is
	// not a delta), and with several repos Head names each one as repo:sha,
	// joined by "+".
	Base, Head string
	// Files is how many paths the reviewer read, -1 when the round has no
	// findings.yaml.
	Files int
	// Findings are the findings the round raised, high risk first: those it
	// gave an id of its own (r2-f1 is round 2's). Its findings.yaml also holds
	// the earlier asks whose decision it recorded, which are not its findings,
	// so a clean round that closed an ask raised none.
	Findings []Finding
	// Fix, Ask and Note count Findings by the reviewer's own label.
	Fix, Ask, Note int
	// FixSlices are the fix slices the round appended.
	FixSlices []string
}

var (
	roundDirRE  = regexp.MustCompile(`^round-(\d+)$`)
	findingIDRE = regexp.MustCompile(`^r(\d+)-`)
)

// raisedIn is the round that gave f its id (r2-f1 was raised in round 2), else
// round, the one whose findings.yaml holds it.
func raisedIn(f Finding, round int) int {
	if m := findingIDRE.FindStringSubmatch(f.ID); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return round
}

// ReadRounds reads every gate round ticket has recorded, oldest first. A round
// whose report.yaml cannot be read or parsed is left out: `jig status` names
// those already (its unreadable_gate_rounds), and one corrupt round is no
// reason to show none. A findings.yaml that cannot be read leaves its round
// with no findings.
func ReadRounds(st *store.Store, ticket string) ([]RoundRecord, error) {
	entries, err := os.ReadDir(filepath.Join(st.TicketDir(ticket), "gate"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("verifydeliver: rounds: %w", err)
	}
	var numbers []int
	for _, e := range entries {
		if m := roundDirRE.FindStringSubmatch(e.Name()); m != nil && e.IsDir() {
			if n, err := strconv.Atoi(m[1]); err == nil {
				numbers = append(numbers, n)
			}
		}
	}
	sort.Ints(numbers)
	if len(numbers) == 0 {
		return nil, nil
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: rounds: read journal: %w", err)
	}

	var out []RoundRecord
	priorHead := ""
	for _, n := range numbers {
		data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, n), "report.yaml"))
		if err != nil {
			continue
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			continue
		}
		rec := RoundRecord{Round: n, Independent: rep.Independent, FixSlices: rep.FixSlices, Files: -1}
		switch rep.Verdict {
		case RoundClean:
			rec.Verdict = RoundClean
		case VerdictNotIndependent:
			rec.Verdict = VerdictNotIndependent
		default:
			rec.Verdict = RoundFindings
		}
		rec.Model, rec.Effort = rep.Model, rep.Effort
		if rep.Reviewer != nil {
			rec.Reviewer = *rep.Reviewer
		}
		journaledReviewerOf(lines, n, &rec)
		rec.Harness = harnessOf(lines, rec.Reviewer.Session)

		if ff, ok, err := readFindingsYAML(st, ticket, n); err == nil && ok {
			rec.Scope, rec.Files = ff.Scope, len(ff.ReviewedPaths)
			for _, f := range ff.Findings {
				if raisedIn(f, n) == n {
					rec.Findings = append(rec.Findings, f)
				}
			}
			sortByRiskThenID(rec.Findings)
			for _, f := range rec.Findings {
				switch f.Action {
				case ActionFix:
					rec.Fix++
				case ActionAsk:
					rec.Ask++
				case ActionNote:
					rec.Note++
				}
			}
		}
		rec.Head = reviewedHeadOf(rep)
		rec.Base = roundBase(st, ticket, n, rec.Scope, priorHead, rec.Head)
		priorHead = rec.Head
		out = append(out, rec)
	}
	return out, nil
}

// journaledReviewerOf fills what rec lacks of its reviewer from the journal's
// lines of round n: the gate-reviewer line when the reviewer named themselves
// as they opened it, and the line that closed the round.
func journaledReviewerOf(lines []journal.Line, n int, rec *RoundRecord) {
	for _, l := range lines {
		switch l.Event {
		case journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean:
		default:
			continue
		}
		if l.Attempt != n {
			continue
		}
		if rec.Reviewer.Session == "" {
			rec.Reviewer.Session, rec.Reviewer.Agent = l.Session, l.Agent
		}
		if rec.Model == "" {
			rec.Model = l.Model
		}
		if rec.Effort == "" {
			rec.Effort = l.Effort
		}
	}
	if rec.Model == "" {
		rec.Model = rec.Reviewer.Model
	}
	if rec.Effort == "" {
		rec.Effort = rec.Reviewer.Effort
	}
}

// harnessOf is the harness a session's journal lines name, "" for none.
func harnessOf(lines []journal.Line, session string) string {
	if session == "" {
		return ""
	}
	for _, l := range lines {
		if l.Session == session && l.Harness != "" {
			return l.Harness
		}
	}
	return ""
}

// reviewedHeadOf is the head a round reviewed: the head the reviewer read, else
// the head the gate ran its oracles on. With several repos it names each.
func reviewedHeadOf(rep reportYAML) string {
	heads := rep.ReviewedSHA
	if len(heads) == 0 {
		heads = rep.TargetSHA
	}
	return namedSHAs(heads)
}

// namedSHAs is the sha of a round over one repo as it is, and of a round over
// several as repo:sha for each, joined by "+" in the order of the repos' names.
func namedSHAs(shas map[string]string) string {
	switch len(shas) {
	case 0:
		return ""
	case 1:
		for _, sha := range shas {
			return sha
		}
	}
	names := make([]string, 0, len(shas))
	for name := range shas {
		names = append(names, name)
	}
	sort.Strings(names)
	joined := ""
	for i, name := range names {
		if i > 0 {
			joined += "+"
		}
		joined += name + ":" + shas[name]
	}
	return joined
}

// roundBase is where round n's review started: the base its request recorded
// when the request is still in the work folder (with several repos, the base of
// each, named as the head is), else the head the round before reviewed when this
// round's scope is a delta, else nothing is known.
func roundBase(st *store.Store, ticket string, n int, scope, priorHead, head string) string {
	if data, err := os.ReadFile(reviewJSONPath(st, ticket, n)); err == nil {
		var req struct {
			BaseSHA string `json:"base_sha"`
			HeadSHA string `json:"head_sha"`
			Repos   []struct {
				Repo    string `json:"repo"`
				BaseSHA string `json:"base_sha"`
				HeadSHA string `json:"head_sha"`
			} `json:"repos"`
		}
		if json.Unmarshal(data, &req) == nil {
			if len(req.Repos) > 0 {
				bases, heads := map[string]string{}, map[string]string{}
				for _, r := range req.Repos {
					bases[r.Repo], heads[r.Repo] = r.BaseSHA, r.HeadSHA
				}
				if namedSHAs(heads) == head {
					return namedSHAs(bases)
				}
			} else if req.BaseSHA != "" && (req.HeadSHA == "" || req.HeadSHA == head) {
				return req.BaseSHA
			}
		}
	}
	if scope == "delta" {
		return priorHead
	}
	return ""
}
