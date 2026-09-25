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

// verdictEntryJSON and verdictsFileJSON are verdicts.json's exact wire
// shape.
type verdictEntryJSON struct {
	Candidate int  `json:"candidate"`
	Same      bool `json:"same"`
}

type verdictsFileJSON struct {
	Verdicts []verdictEntryJSON `json:"verdicts"`
}

// judgePromptTemplate states the job and the output contract only: no
// kinds of problems, no coaching, the same restraint
// verifydeliver.reviewPromptTemplate holds to.
const judgePromptTemplate = `You are judging round %d of case %s. Your input is %s: a list of candidates, each a point (a seeded finding, trap, decision or earlier dismissed finding) paired with one of this round's reported findings that structurally sits near it.
For each candidate, decide whether the finding is describing the SAME underlying problem as the point, or a DIFFERENT one that merely sits near it in the file.
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

// readVerdicts parses verdictsPath strictly: exactly one JSON object, no
// unknown keys, every candidate in [0,n) answered exactly once.
func readVerdicts(verdictsPath string, n int) ([]Verdict, error) {
	data, err := os.ReadFile(verdictsPath)
	if err != nil {
		return nil, fmt.Errorf("revieweval: judge: read verdicts.json: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wire verdictsFileJSON
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("revieweval: judge: parse verdicts.json: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("revieweval: judge: verdicts.json must contain exactly one JSON object")
	}

	out := make([]Verdict, n)
	seen := make([]bool, n)
	for _, v := range wire.Verdicts {
		if v.Candidate < 0 || v.Candidate >= n {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d is out of range [0,%d)", v.Candidate, n)
		}
		if seen[v.Candidate] {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d answered more than once", v.Candidate)
		}
		seen[v.Candidate] = true
		if v.Same {
			out[v.Candidate] = Same
		} else {
			out[v.Candidate] = Different
		}
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("revieweval: judge: verdicts.json: candidate %d was never answered", i)
		}
	}
	return out, nil
}
