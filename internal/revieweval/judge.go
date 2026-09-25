package revieweval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/session"
)

// ModelJudge confirms structural candidates with a real model: one session
// dispatch per round, asked to decide every candidate at once.
type ModelJudge struct {
	Backend session.Backend
	Model   string
}

// judgeCandidateJSON is one candidate's line in judge.json: the point it
// might be and the finding structure paired it with, everything the judge
// needs to decide same or different with no access to the diff beyond
// what is already here.
type judgeCandidateJSON struct {
	Index            int    `json:"index"`
	PointFile        string `json:"point_file"`
	PointFrom        int    `json:"point_from"`
	PointTo          int    `json:"point_to"`
	PointDescription string `json:"point_description"`
	FindingFile      string `json:"finding_file"`
	FindingLine      int    `json:"finding_line"`
	FindingTitle     string `json:"finding_title"`
	FindingDetail    string `json:"finding_detail"`
}

type judgeFileJSON struct {
	Candidates []judgeCandidateJSON `json:"candidates"`
}

// judgePromptTemplate states the job and the output contract only: no
// kinds of problems, no coaching, the same restraint
// verifydeliver.reviewPromptTemplate holds to. Every candidate asks the
// judge the same one question, whatever kind of point it pairs a finding
// with (a seeded finding, a trap, a decision, or an earlier dismissed
// finding) - the prompt never names or distinguishes those kinds, so the
// judge cannot use which kind a point is as a shortcut for whether it
// matches.
const judgePromptTemplate = `You are judging round %d of case %s. Your input is %s: a list of candidates, each a point (a problem description) paired with one of this round's reported findings that structurally sits near it.
For each candidate, decide whether the finding raises the SAME problem as the point's description, or a DIFFERENT one that merely sits near it in the file.
When finished, write %s with exactly one JSON object: {"verdicts": [{"candidate": 0, "same": true}]}, exactly one entry per candidate index, in any order, no unknown keys.`

// Confirm writes judge.json under q.WorkDir, dispatches one session, and
// reads verdicts.json back strictly. A dispatch error or an invalid file
// is returned as an error; the caller (runner.go) turns it into a failed
// round.
func (j *ModelJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	if err := os.MkdirAll(q.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("revieweval: judge: create work dir: %w", err)
	}

	var file judgeFileJSON
	for i, c := range q.Candidates {
		f := q.Findings[c.Finding]
		file.Candidates = append(file.Candidates, judgeCandidateJSON{
			Index:     i,
			PointFile: c.Point.File, PointFrom: c.Point.From, PointTo: c.Point.To, PointDescription: c.Point.Description,
			FindingFile: f.File, FindingLine: f.Line, FindingTitle: f.Title, FindingDetail: f.Detail,
		})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("revieweval: judge: marshal judge.json: %w", err)
	}
	judgePath := filepath.Join(q.WorkDir, "judge.json")
	if err := os.WriteFile(judgePath, data, 0o644); err != nil {
		return nil, fmt.Errorf("revieweval: judge: write judge.json: %w", err)
	}

	verdictsPath := filepath.Join(q.WorkDir, "verdicts.json")
	// A failed earlier attempt at this round's judge call must never be
	// read back as this attempt's verdicts, the same guard
	// reviewerGateSource.Round keeps over a stale result.json.
	if err := os.Remove(verdictsPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("revieweval: judge: remove stale verdicts.json: %w", err)
	}

	prompt := fmt.Sprintf(judgePromptTemplate, q.Round, q.Case, judgePath, verdictsPath)
	dispatch := session.Dispatch{
		Ticket:     q.Case,
		Slice:      "judge",
		Attempt:    q.Round,
		Worktree:   q.RepoDir,
		SliceJSON:  judgePath,
		ResultJSON: verdictsPath,
		Model:      j.Model,
		Prompt:     prompt,
		Screen:     true,
	}
	if err := j.Backend.Run(dispatch); err != nil {
		return nil, fmt.Errorf("revieweval: judge: dispatch: %w", err)
	}

	return readVerdicts(verdictsPath, len(q.Candidates))
}

// strictObjectKeys reads one JSON object from dec (whose next token must be
// its opening brace) and returns each key's own raw value, rejecting a key
// not in allowed (checked case-sensitively, so a case variant is also
// unknown) and a key repeated within this one object; encoding/json's own
// struct and map decoding do neither on its own (a struct falls back to a
// case-insensitive field match, and both silently let a later duplicate
// key win over an earlier one).
func strictObjectKeys(dec *json.Decoder, allowed ...string) (map[string]json.RawMessage, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object, got %v", tok)
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string key, got %v", keyTok)
		}
		if !allowedSet[key] {
			return nil, fmt.Errorf("unknown key %q", key)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("key %q repeated in one object", key)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out[key] = raw
	}
	if _, err := dec.Token(); err != nil { // the closing '}'
		return nil, err
	}
	return out, nil
}

// isJSONNull reports whether raw is exactly the JSON literal null.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// readVerdicts parses verdictsPath strictly: exactly one JSON object
// at the top level; its only allowed key is "verdicts", an array of
// objects whose only allowed keys are "candidate" and "same" (case-
// sensitive: a variant like "Same" is an unknown key, not a match);
// "candidate" and "same" must both be present and non-null in every entry;
// no key repeats within one object; every candidate in [0,n) is answered
// exactly once.
func readVerdicts(verdictsPath string, n int) ([]Verdict, error) {
	data, err := os.ReadFile(verdictsPath)
	if err != nil {
		return nil, fmt.Errorf("revieweval: judge: read verdicts.json: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	top, err := strictObjectKeys(dec, "verdicts")
	if err != nil {
		return nil, fmt.Errorf("revieweval: judge: parse verdicts.json: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("revieweval: judge: verdicts.json must contain exactly one JSON object")
	}

	rawVerdicts, ok := top["verdicts"]
	if !ok || isJSONNull(rawVerdicts) {
		return nil, fmt.Errorf("revieweval: judge: verdicts.json: \"verdicts\" is missing or null")
	}
	entryDec := json.NewDecoder(bytes.NewReader(rawVerdicts))
	tok, err := entryDec.Token()
	if err != nil {
		return nil, fmt.Errorf("revieweval: judge: verdicts.json: parse \"verdicts\": %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("revieweval: judge: verdicts.json: \"verdicts\" must be an array")
	}

	out := make([]Verdict, n)
	seen := make([]bool, n)
	for entryDec.More() {
		entry, err := strictObjectKeys(entryDec, "candidate", "same")
		if err != nil {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: a verdict entry: %w", err)
		}
		rawCandidate, ok := entry["candidate"]
		if !ok || isJSONNull(rawCandidate) {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: a verdict entry is missing \"candidate\"")
		}
		rawSame, ok := entry["same"]
		if !ok || isJSONNull(rawSame) {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: a verdict entry is missing \"same\"")
		}
		var candidate int
		if err := json.Unmarshal(rawCandidate, &candidate); err != nil {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: \"candidate\" is not a number: %w", err)
		}
		var same bool
		if err := json.Unmarshal(rawSame, &same); err != nil {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: \"same\" is not a boolean: %w", err)
		}

		if candidate < 0 || candidate >= n {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d is out of range [0,%d)", candidate, n)
		}
		if seen[candidate] {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d answered more than once", candidate)
		}
		seen[candidate] = true
		if same {
			out[candidate] = Same
		} else {
			out[candidate] = Different
		}
	}
	if _, err := entryDec.Token(); err != nil { // the closing ']'
		return nil, fmt.Errorf("revieweval: judge: verdicts.json: parse \"verdicts\": %w", err)
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d was never answered", i)
		}
	}
	return out, nil
}
