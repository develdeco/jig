package verifydeliver

import (
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// Independence says whether a recorded round's reviewer is independent of the
// agents that built what it reviewed. jig starts no reviewer, so it cannot
// make one fresh; it can only notice, from the records of who did what, that
// the reviewer is one of the builders.
type Independence string

const (
	// IndependentYes: the reviewer is known and is none of the builders.
	IndependentYes Independence = "true"
	// IndependentNo: the reviewer is one of the builders. The round is
	// recorded but is not clean (REVIEW_NOT_INDEPENDENT), and publish refuses
	// it.
	IndependentNo Independence = "false"
	// IndependentUnknown: jig could not tell who reviewed. The round stands,
	// counted as unknown and said so; publish accepts it, because refusing
	// would make a missing hook the end of every review.
	IndependentUnknown Independence = "unknown"
)

// MarshalYAML writes true and false as YAML booleans (`independent: true`)
// and unknown as the plain word.
func (i Independence) MarshalYAML() (any, error) {
	tag := "!!str"
	if i == IndependentYes || i == IndependentNo {
		tag = "!!bool"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(i)}, nil
}

// UnmarshalYAML reads what MarshalYAML writes.
func (i *Independence) UnmarshalYAML(n *yaml.Node) error {
	*i = Independence(strings.ToLower(strings.TrimSpace(n.Value)))
	return nil
}

// How jig learned who a reviewer is, in Provenance.How.
const (
	// ReviewerByFlag: `--session` (and `--agent-id`) on the verb that closed
	// the round.
	ReviewerByFlag = "flag"
	// ReviewerByEnv: the session the environment of the verb that closed the
	// round names ($JIG_SESSION, else the harness's own variable). It names
	// the harness session and never the agent in it, since a subagent
	// inherits its parent's environment.
	ReviewerByEnv = "env"
	// ReviewerRecorded: the session the reviewer named when it opened the
	// round with `jig review context --session`.
	ReviewerRecorded = "recorded"
	// ReviewerByLedger: the machine ledger's record of a tool call that ran
	// `jig review context` for the ticket after the round opened.
	ReviewerByLedger = "ledger"
)

// Provenance is who reported a recorded round: the harness session and the
// agent within it (a subagent's own id, "" for the session itself), how jig
// learned that, and what the reviewer said it ran on. The zero Session is a
// reviewer jig could not identify.
type Provenance struct {
	Session string `yaml:"session,omitempty" json:"session,omitempty"`
	Agent   string `yaml:"agent,omitempty" json:"agent,omitempty"`
	How     string `yaml:"how,omitempty" json:"how,omitempty"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
	Effort  string `yaml:"effort,omitempty" json:"effort,omitempty"`
}

// Known reports whether the reviewer is identified.
func (p Provenance) Known() bool { return p.Session != "" }

// agentID is one agent: a session and an agent within it.
type agentID struct{ session, agent string }

// Builders are the agents that built what a ticket's branch holds, from the
// records of the work: the session and agent on the ticket journal's claim,
// verify-pending and oracle lines, and the machine ledger's git-commit lines
// and tool lines that changed files, for the ticket (or its branch). A tool
// line that changed no file (a read, a search, `git log`) is no building: a
// reviewer reads the code with the same tools.
func Builders(d Deps, ticket, branch string) ([]Provenance, error) {
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return nil, err
	}
	seen := map[agentID]bool{}
	var out []Provenance
	add := func(session, agent string) {
		id := agentID{session, agent}
		if session == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, Provenance{Session: session, Agent: agent})
	}
	for _, l := range lines {
		switch l.Event {
		case journal.EventClaim, journal.EventVerifyPending, journal.EventOracle:
			add(l.Session, l.Agent)
		}
	}
	if d.Home == "" {
		return out, nil
	}
	records, err := ledger.Read(d.Home)
	if err != nil {
		return nil, err
	}
	for _, l := range records {
		if !(l.Ticket == ticket || (branch != "" && l.Branch == branch)) {
			continue
		}
		switch {
		case l.Event == journal.EventGitCommit, l.Event == journal.EventTool && len(l.Files) > 0:
			add(l.Session, l.Agent)
		}
	}
	return out, nil
}

// JudgeIndependence says whether reviewer is independent of the builders:
// unknown when the reviewer is not identified, false when it is one of them (the
// same session and the same agent), true otherwise.
func JudgeIndependence(d Deps, ticket, branch string, reviewer Provenance) (Independence, error) {
	if !reviewer.Known() {
		return IndependentUnknown, nil
	}
	builders, err := Builders(d, ticket, branch)
	if err != nil {
		return "", err
	}
	for _, b := range builders {
		if b.Session == reviewer.Session && b.Agent == reviewer.Agent {
			return IndependentNo, nil
		}
	}
	return IndependentYes, nil
}

// LedgerReviewer finds the reviewer of the round a ticket opened at opened
// when nobody named them: the latest ledger tool line whose command ran `jig
// review context` for the ticket after the round opened. A reviewer's harness
// writes that line when the reviewer reads the round, so it is the one record
// of who read it. ok is false when there is none.
func LedgerReviewer(d Deps, ticket string, opened time.Time) (Provenance, bool, error) {
	if d.Home == "" {
		return Provenance{}, false, nil
	}
	records, err := ledger.Read(d.Home)
	if err != nil {
		return Provenance{}, false, err
	}
	var best Provenance
	var bestAt time.Time
	found := false
	for _, l := range records {
		if l.Event != journal.EventTool || l.Session == "" || !runsReviewContext(l.Command, ticket) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.TS)
		if err != nil || at.Before(opened) {
			continue
		}
		if !found || at.After(bestAt) {
			best, bestAt, found = Provenance{Session: l.Session, Agent: l.Agent, How: ReviewerByLedger}, at, true
		}
	}
	return best, found, nil
}

// LedgerReviewerAgent is the agent of session that read ticket's open round:
// the agent on the machine ledger's records of tool calls in that session
// that ran `jig review context` for the ticket after the round opened, when
// they all name the same one. Records naming two agents (the session itself
// and a subagent, or two subagents) say nothing about which one closes the
// round, and neither does no record: ok is false.
func LedgerReviewerAgent(d Deps, ticket string, opened time.Time, session string) (agent string, ok bool, err error) {
	if d.Home == "" || session == "" {
		return "", false, nil
	}
	records, err := ledger.Read(d.Home)
	if err != nil {
		return "", false, err
	}
	for _, l := range records {
		if l.Event != journal.EventTool || l.Session != session || !runsReviewContext(l.Command, ticket) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.TS)
		if err != nil || at.Before(opened) {
			continue
		}
		if ok && l.Agent != agent {
			return "", false, nil
		}
		agent, ok = l.Agent, true
	}
	return agent, ok, nil
}

// runsReviewContext reports whether the shell command line cmd runs `jig
// review context` for ticket: jig (any path, with or without .exe), then
// review and context, then the ticket among the words up to the next shell
// separator, or no ticket word at all (the verb then takes the ticket from the
// branch the clone is on).
func runsReviewContext(cmd, ticket string) bool {
	words := strings.Fields(cmd)
	for i, w := range words {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/")))
		if base != "jig" && base != "jig.exe" {
			continue
		}
		if i+2 >= len(words) || words[i+1] != "review" || words[i+2] != "context" {
			continue
		}
		positional := false
		for _, a := range words[i+3:] {
			switch a {
			case "&&", "||", ";", "|":
				return !positional
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			positional = true
			if strings.Trim(a, `"'`) == ticket {
				return true
			}
		}
		return !positional
	}
	return false
}
