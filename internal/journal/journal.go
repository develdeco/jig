// Package journal is the append-only NDJSON record of everything jig does
// to a ticket, and the pure renderers that turn it into human-readable
// changelogs.
package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/store"
)

// Line is one NDJSON record in a ticket's journal. Wire keys are exact.
type Line struct {
	TS      string `json:"ts"` // RFC3339; filled by Append if empty
	Ticket  string `json:"ticket"`
	Slice   string `json:"slice,omitempty"`
	Event   string `json:"event"`
	Outcome string `json:"outcome,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Model   string `json:"model,omitempty"`
	// Effort is the reasoning effort a session was dispatched with: on a
	// builder's dispatch line, and on a gate round's own line for its
	// reviewer ("" when none was passed).
	Effort  string `json:"effort,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	// Command and Env are an oracle line's exact command and the env class
	// that was up while it ran ("" for none): what the gate must match to
	// reuse the run (ADR 0021).
	Command string `json:"command,omitempty"`
	Env     string `json:"env,omitempty"`
	// Seconds is an oracle line's wall time, in whole seconds: what the next
	// builder of the same command is told (ADR 0024).
	Seconds int `json:"seconds,omitempty"`
	// Head is a result line's lease head when that turn of the attempt
	// ended: with the attempt's dispatch line's commit (its base), the
	// range of the attempt's own commits (ADR 0025).
	Head string `json:"head,omitempty"`
	// Recordings is a recorded line's files: what the build's end-to-end
	// scenarios wrote at a builder's green oracle run, with the commit it
	// ran at in Commit (ADR 0029).
	Recordings []Recording `json:"recordings,omitempty"`
}

// Recording is one file a scenario recorded: its name inside the recording
// directory under the jig home, its hash and size as jig checked them, and
// the scenario, optional flow and step, and caption its writer tagged it with.
type Recording struct {
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Scenario string `json:"scenario"`
	Flow     string `json:"flow,omitempty"`
	Step     int    `json:"step,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

func journalPath(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "journal.ndjson")
}

// Append writes l as one NDJSON line to <ticket>/journal.ndjson, under a
// lock on the journal file. l.Ticket is set to ticket, and l.TS defaults to
// now (RFC3339) when empty.
func Append(st *store.Store, ticket string, l Line) error {
	if l.TS == "" {
		l.TS = time.Now().UTC().Format(time.RFC3339)
	}
	l.Ticket = ticket

	path := journalPath(st, ticket)
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	encoded, err := json.Marshal(l)
	if err != nil {
		return err
	}
	data = append(data, encoded...)
	data = append(data, '\n')

	return store.AtomicWrite(path, data)
}

// Read returns every line of a ticket's journal, in append order. Readers
// never lock. An absent journal reads as no lines.
func Read(st *store.Store, ticket string) ([]Line, error) {
	data, err := os.ReadFile(journalPath(st, ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var lines []Line
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var l Line
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// FailedAttempts counts slice's attempts that failed at the work: a result
// line whose outcome is not needs-input, flawed-brief or blocked-by-env, for
// an attempt with no verified line. A question, a flawed brief or a blocked
// environment is not the builder failing, and a green that verified is a
// success; a green that did not verify is a failure. The staircase climbs a
// rung per failed attempt (ADR 0019).
func FailedAttempts(lines []Line, slice string) int {
	verified := map[int]bool{}
	for _, l := range lines {
		if l.Slice == slice && l.Event == "verified" {
			verified[l.Attempt] = true
		}
	}
	failed := map[int]bool{}
	for _, l := range lines {
		if l.Slice != slice || l.Event != "result" || verified[l.Attempt] {
			continue
		}
		switch l.Outcome {
		case "needs-input", "flawed-brief", "blocked-by-env":
			continue
		}
		failed[l.Attempt] = true
	}
	return len(failed)
}

// BuiltCommits returns the commit of every event=verified line, without
// repeats, in journal order: the commits jig's builders reported on the
// ticket's branch that verified. The frontier journals one when a green result's
// commit passes verifyGreen (a descendant of the start sha, reachable from the
// lease's HEAD, its artifacts in its tree), after the result line that records
// what the builder claimed, so a claim that did not verify - a sha that is not
// in the lease, or is the start sha itself, or is off the branch - is not
// among them. The journal is the store's record of them, so it answers
// whether jig built on a ticket from any machine, where a lease answers only
// for the machine it sits on. A journal written before jig recorded
// verifications has none.
func BuiltCommits(lines []Line) []string {
	var commits []string
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Event == "verified" && l.Commit != "" && !seen[l.Commit] {
			seen[l.Commit] = true
			commits = append(commits, l.Commit)
		}
	}
	return commits
}

// GreenClaims returns the commit of every green result line, without repeats,
// in journal order: the commits jig's builders claimed on the ticket's branch,
// whether or not they verified. Every jig version journals the result line
// before it routes the result, and nothing removes it (a requeue changes a
// slice's state, not the journal), so unlike a slice's state it is a record of
// a claim for the journal's whole life, and it is the only one a journal
// written before verified lines has. BuiltCommits is the ones that verified.
func GreenClaims(lines []Line) []string {
	var commits []string
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Event == "result" && l.Outcome == "green" && l.Commit != "" && !seen[l.Commit] {
			seen[l.Commit] = true
			commits = append(commits, l.Commit)
		}
	}
	return commits
}

// LastOracleSeconds is the wall time of the latest oracle run of command,
// with env class env up ("" for none), in lines that recorded one, or 0
// when none did. The same command can take very different times with and
// without an env class, so both must match.
func LastOracleSeconds(lines []Line, command, env string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if l := lines[i]; l.Event == "oracle" && l.Command == command && l.Env == env && l.Seconds > 0 {
			return l.Seconds
		}
	}
	return 0
}

// VerifiedSlice is a slice whose green jig verified, with the ranges of its
// attempts' own commits (VerifiedSlices).
type VerifiedSlice struct {
	Slice string
	// Attempt is the slice's latest verified attempt.
	Attempt int
	// Ranges are base..end pairs, one per attempt of the slice that has a
	// dispatch base and an end: the base is the dispatch line's commit, and
	// the end the attempt's verified commit or else its last result line's
	// head. Other slices may run between two attempts, so only these ranges
	// hold the slice's own commits.
	Ranges [][2]string
}

// VerifiedSlices lists the slices in lines whose green jig verified, other
// than skip, in the order they first verified.
func VerifiedSlices(lines []Line, skip string) []VerifiedSlice {
	type attemptKey struct {
		slice   string
		attempt int
	}
	base := map[attemptKey]string{}
	end := map[attemptKey]string{}
	verified := map[attemptKey]bool{}
	var order []string
	latest := map[string]int{}
	attempts := map[string][]int{}
	for _, l := range lines {
		k := attemptKey{l.Slice, l.Attempt}
		switch l.Event {
		case "dispatch":
			base[k] = l.Commit
			attempts[l.Slice] = append(attempts[l.Slice], l.Attempt)
		case "result":
			if l.Head != "" && !verified[k] {
				end[k] = l.Head
			}
		case "verified":
			if l.Slice == skip {
				continue
			}
			verified[k] = true
			if l.Commit != "" {
				end[k] = l.Commit
			}
			if _, ok := latest[l.Slice]; !ok {
				order = append(order, l.Slice)
			}
			latest[l.Slice] = l.Attempt
		}
	}
	out := make([]VerifiedSlice, 0, len(order))
	for _, s := range order {
		v := VerifiedSlice{Slice: s, Attempt: latest[s]}
		for _, a := range attempts[s] {
			k := attemptKey{s, a}
			if base[k] != "" && end[k] != "" && base[k] != end[k] {
				v.Ranges = append(v.Ranges, [2]string{base[k], end[k]})
			}
		}
		out = append(out, v)
	}
	return out
}
