// Package journal is the append-only NDJSON record of everything jig does
// to a ticket, and the pure renderers that turn it into human-readable
// changelogs.
package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// Line is one NDJSON record in a ticket's journal. Wire keys are exact.
type Line struct {
	TS      string `json:"ts"` // RFC3339 with fractional seconds; filled by Append if empty
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
	// ran at in Commit (ADR 0029). RecordRun names the oracle run, one
	// directory below the commit's, so a reader finds the files at
	// home.RecordDir(root, storeID, ticket, Commit, RecordRun).
	RecordRun  string      `json:"record_run,omitempty"`
	Recordings []Recording `json:"recordings,omitempty"`
	// Pick is a publish-picks line's choice: which of the build's recordings
	// a publish picked for the pull request, composed into flows, with the
	// head it picked for in Commit and the line's Outcome saying whether it
	// was newly picked, reused or refused (ADR 0029).
	Pick *Pick `json:"pick,omitempty"`

	// The fields below belong to the digester's records (the journal's
	// claim, driver, note, spend and approval lines and the machine
	// ledger's session and tool lines). Each is omitted when empty, so a
	// line an older jig wrote reads back with none and a line that carries
	// none writes none.

	// Session is the harness session id the line was written for; Agent the
	// id of the agent within it (a subagent's own id, "" for the session
	// itself); Harness the harness's name (claude, codex, ...).
	Session string `json:"session,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Harness string `json:"harness,omitempty"`
	// Source is who ran an oracle line's command: "jig" (a verify worker)
	// or "session" (the session ran it itself and jig observed it).
	Source string `json:"source,omitempty"`
	// Signature is a failure's normalized signature, the key repeat
	// failures are matched on. Summary is the failure's flattened tail,
	// at most 600 bytes; the full tail lives in a machine-local log.
	Signature string `json:"signature,omitempty"`
	Summary   string `json:"summary,omitempty"`
	// Provenance says who a line speaks for: "person", "agent" or "jig".
	Provenance string `json:"provenance,omitempty"`
	// Unattended is set on a claim line made without a person watching.
	Unattended bool `json:"unattended,omitempty"`
	// Branch and URL name a branch or a link the line is about.
	Branch string `json:"branch,omitempty"`
	URL    string `json:"url,omitempty"`
	// Ref is the id of the subject the line is about: a question (q-001),
	// a review finding (r1-f2) or a note's fingerprint.
	Ref string `json:"ref,omitempty"`
	// Tool is the harness tool a ledger tool line records; Cwd the
	// directory it ran in; Exit its exit status when it had one; Files the
	// files it touched.
	Tool  string   `json:"tool,omitempty"`
	Cwd   string   `json:"cwd,omitempty"`
	Exit  *int     `json:"exit,omitempty"`
	Files []string `json:"files,omitempty"`
	// PID is the process id a ledger line records (a spawned verify
	// worker's, say).
	PID int `json:"pid,omitempty"`
	// Spend is what a spend line records: tokens and dollars.
	Spend *Spend `json:"spend,omitempty"`
}

// Spend is a spend line's cost: the token counts a transcript's usage
// records sum to, and the dollars they priced at.
type Spend struct {
	Input      int64   `json:"input,omitempty"`
	Output     int64   `json:"output,omitempty"`
	CacheRead  int64   `json:"cache_read,omitempty"`
	CacheWrite int64   `json:"cache_write,omitempty"`
	USD        float64 `json:"usd,omitempty"`
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

// Pick is what a publish-picks line records of a pick: Candidates fingerprints
// the candidate recordings the pick was made from (so a re-publish of the same
// head with the same candidates reuses it), Summary is the session's account
// of what the recordings show, and Flows are the picked recordings composed
// into flows, in the order the pull request shows them.
type Pick struct {
	Candidates string     `json:"candidates"`
	Summary    string     `json:"summary"`
	Flows      []PickFlow `json:"flows"`
}

// PickFlow is one flow of a pick: a title and the recordings in it, in order.
type PickFlow struct {
	Title string     `json:"title"`
	Items []PickItem `json:"items"`
}

// PickItem is one picked recording: the id the pick session named it by, the
// file it is in its recording directory, and the caption the session gave it in
// place of the recording's own (empty when it kept that one).
type PickItem struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Caption string `json:"caption,omitempty"`
}

func journalPath(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "journal.ndjson")
}

// lockedCode is Append's refusal when it cannot get the journal's lock.
const lockedCode = "JOURNAL_LOCKED"

// appendLockTimeout is how long Append waits for another writer of the same
// journal before it refuses.
const appendLockTimeout = 30 * time.Second

// Append writes l as one NDJSON line to <ticket>/journal.ndjson, under a
// lock on the journal file and, outside it, the store's checkpoint lock
// (Store.LockCheckpoint), so a detached worker's append never lands in the
// middle of a Sync or Push another process is making. l.Ticket is set to
// ticket, and l.TS defaults to now (RFC3339 with nanoseconds) when empty.
//
// It never writes without the file lock: if another writer still holds it
// after thirty seconds Append refuses with JOURNAL_LOCKED, because a line
// written unlocked is how two writers lose each other's lines. The line goes
// out in a single O_APPEND write, with a newline in front of it when the
// file ends in a torn line (a writer that died mid-line), so that writer
// costs its own line and not this one. The file is appended to in place and
// never rewritten, so a reader that opened it earlier sees at most a torn
// final line, which Read leaves out.
func Append(st *store.Store, ticket string, l Line) error {
	return appendWithin(st, ticket, l, appendLockTimeout)
}

// appendWithin is Append waiting up to wait for the journal lock, so a test
// can see the refusal without waiting half a minute.
func appendWithin(st *store.Store, ticket string, l Line, wait time.Duration) error {
	if l.TS == "" {
		l.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	l.Ticket = ticket
	encoded, err := json.Marshal(l)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	unlock, err := st.LockCheckpoint()
	if err != nil {
		return err
	}
	defer unlock()

	path := journalPath(st, ticket)
	release, held, err := store.TryLock(path, wait)
	if err != nil {
		return err
	}
	defer release()
	if !held {
		return &axi.Error{
			Msg:  fmt.Sprintf("the journal %s is locked by another process and stayed locked for %s", path, wait),
			Code: lockedCode,
			Help: []string{"Retry; if it persists, a jig process on this machine is stuck holding it"},
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if torn, err := endsMidLine(f); err != nil {
		return err
	} else if torn {
		encoded = append([]byte{'\n'}, encoded...)
	}
	_, err = f.Write(encoded)
	return err
}

// endsMidLine reports whether f is non-empty and does not end in a newline.
func endsMidLine(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// Read returns every line of a ticket's journal, in append order. Readers
// never lock. An absent journal reads as no lines.
//
// A line that is not a JSON line is left out, and so is a blank one: a final
// line a writer is still writing, or one a writer died in the middle of, must
// not hide the lines before it from every reader, and the next Append closes
// a torn line off with a newline, which makes it a bad line in the middle.
// The lines of a ticket's journal are appended by several writers and merged
// by git, so a reader must not assume they are in time order either.
func Read(st *store.Store, ticket string) ([]Line, error) {
	data, err := os.ReadFile(journalPath(st, ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var lines []Line
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var l Line
		if err := json.Unmarshal(raw, &l); err != nil {
			continue
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
