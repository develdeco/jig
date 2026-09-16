package make

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/outcome"
	"github.com/develdeco/jig/store"
)

// dispatchPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for
// every build-session dispatch. The fake backend ignores it entirely; it is
// still rendered so the headless/herdr backends and the recorded journal
// carry it.
const dispatchPromptTemplate = "You are a jig build session for slice %s of ticket %s.\n" +
	"Work ONLY in this worktree. Goal: %s\n" +
	"Oracle (green = done): %s\n" +
	"Read your inputs from slice.json at %s (brief sections by path, attempt log, prior answer).\n" +
	"Commit as you land. When finished write result.json at %s with exactly one JSON object: {\"outcome\": \"green|code-bug|flawed-brief|oracle-wrong|blocked-by-env|needs-input|failed\", \"summary\": \"...\", \"commit\": \"<sha>\", \"question\": \"only for needs-input\", \"artifacts\": [\"relative paths\"]}"

// renderDispatchPrompt fills dispatchPromptTemplate for one slice attempt.
func renderDispatchPrompt(id, ticket, goal, oracleCmd, sliceJSONPath, resultJSONPath string) string {
	return fmt.Sprintf(dispatchPromptTemplate, id, ticket, goal, oracleCmd, sliceJSONPath, resultJSONPath)
}

// sliceJSONBody is the exact wire shape jig writes to slice.json before
// dispatching a build session.
type sliceJSONBody struct {
	ID            string   `json:"id"`
	Goal          string   `json:"goal"`
	Oracle        string   `json:"oracle"`
	Workspace     string   `json:"workspace"`
	Env           string   `json:"env"`
	Attempt       int      `json:"attempt"`
	BriefSections []string `json:"brief_sections"`
	AttemptLog    []string `json:"attempt_log"`
	Answer        string   `json:"answer"`
}

// workDir returns the store-side (not lease-side) directory that carries
// every slice's attempt inputs/outputs for ticket. It lives under the store
// so the LEASE's own `git add -A` (the fake backend commits everything in
// the worktree) never sees these files.
func workDir(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), "work")
}

// sliceJSONPath and resultJSONPath return the store-side paths for one
// slice attempt's input and output files.
func sliceJSONPath(st *store.Store, ticket, slice string, attempt int) string {
	return filepath.Join(workDir(st, ticket), fmt.Sprintf("%s.attempt-%d.slice.json", slice, attempt))
}

func resultJSONPath(st *store.Store, ticket, slice string, attempt int) string {
	return filepath.Join(workDir(st, ticket), fmt.Sprintf("%s.attempt-%d.result.json", slice, attempt))
}

// writeSliceJSON marshals body and writes it to path, creating the store's
// work directory if needed.
func writeSliceJSON(path string, body sliceJSONBody) error {
	if body.BriefSections == nil {
		body.BriefSections = []string{}
	}
	if body.AttemptLog == nil {
		body.AttemptLog = []string{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// buildAttemptLog renders "attempt <n> · <summary> → <outcome>" for every
// prior, non-success attempt of slice (attempts 1..beforeAttempt-1), read
// back from the store-side result.json files of those attempts (the journal
// line schema carries no summary field, so the attempt log is built from the
// persisted results instead - see NOTE in make/make.go). Capped to the most
// recent 8 entries.
func buildAttemptLog(st *store.Store, ticket, slice string, beforeAttempt int) []string {
	var log []string
	for n := 1; n < beforeAttempt; n++ {
		data, err := os.ReadFile(resultJSONPath(st, ticket, slice, n))
		if err != nil {
			continue
		}
		res := outcome.ParseJSON("slice", data)
		if outcome.Success[res.Outcome] {
			continue
		}
		log = append(log, fmt.Sprintf("attempt %d · %s → %s", n, res.Summary, res.Outcome))
	}
	if len(log) > 8 {
		log = log[len(log)-8:]
	}
	return log
}

// invertBriefHashes reads ticket's brief.md and returns its section hashes
// inverted: hash -> heading.
func invertBriefHashes(st *store.Store, ticket string) map[string]string {
	data, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if err != nil {
		return nil
	}
	hashes := store.BriefSectionHashes(data)
	inv := make(map[string]string, len(hashes))
	for heading, sum := range hashes {
		inv[sum] = heading
	}
	return inv
}

// resolveBriefSections resolves sl's FromBrief hashes to
// "<abs store ticket dir>/brief.md#<heading>" paths, via the current
// brief.md's section hashes. An unknown hash (the section it named has
// since changed or vanished) is skipped.
func resolveBriefSections(st *store.Store, ticket string, sl store.Slice) []string {
	inv := invertBriefHashes(st, ticket)
	if inv == nil {
		return nil
	}
	ticketDir := st.TicketDir(ticket)
	absTicketDir, err := filepath.Abs(ticketDir)
	if err != nil {
		absTicketDir = ticketDir
	}
	briefPath := filepath.Join(absTicketDir, "brief.md")
	var out []string
	for _, h := range sl.FromBrief {
		heading, ok := inv[h]
		if !ok {
			continue
		}
		out = append(out, briefPath+"#"+heading)
	}
	return out
}

// briefHeadingsFor returns the current headings for sl's FromBrief hashes
// (unknown hashes skipped), used to point a flawed-brief question at the
// section(s) that need amending.
func briefHeadingsFor(st *store.Store, ticket string, sl store.Slice) []string {
	inv := invertBriefHashes(st, ticket)
	if inv == nil {
		return nil
	}
	var out []string
	for _, h := range sl.FromBrief {
		if heading, ok := inv[h]; ok {
			out = append(out, heading)
		}
	}
	return out
}

// answerFor returns the most recently answered question's text for slice,
// or "" if none.
func answerFor(st *store.Store, ticket, slice string) string {
	qs, err := st.ReadQuestions(ticket)
	if err != nil {
		return ""
	}
	answer := ""
	for _, q := range qs {
		if q.Slice == slice && q.Status == "answered" {
			answer = q.Answer
		}
	}
	return answer
}

// flawedBriefQuestionBody renders a flawed-brief question's body: the
// session's summary plus a pointer at the brief section(s) to amend and the
// requeue command to run once they are.
func flawedBriefQuestionBody(ticket, summary string, headings []string) string {
	return fmt.Sprintf("%s\namend brief.md section(s) %s; then `jig requeue %s --from-brief-diff`",
		summary, strings.Join(headings, ", "), ticket)
}
