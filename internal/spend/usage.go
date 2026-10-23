package spend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// Totals is what usage records sum to: a session's, when it comes from a
// transcript, or a ticket's, when it comes from the spend lines of its
// sessions (FromLines).
type Totals struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	// USD is the cost of the priced models' usage at list price.
	USD float64
	// Messages counts the distinct API messages a transcript held; Sessions
	// the sessions whose spend lines were added up. Each is set by the
	// function that makes that kind of Totals and is zero in the other.
	Messages int
	Sessions int
	// Models are the models the usage came from, and Unpriced those of them
	// no price is known for: their tokens are in the counts and their dollars
	// are not in USD. Both are sorted.
	Models   []string
	Unpriced []string
}

// Tokens is every token counted: input, output, and the cache writes and
// reads. It is the figure a budget in tokens is held against.
func (t Totals) Tokens() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// Spend is t as a spend line records it.
func (t Totals) Spend() journal.Spend {
	return journal.Spend{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, USD: t.USD}
}

// syntheticModel is the model Claude Code names the messages it writes itself
// (an interrupted turn, an API error): they carry no usage.
const syntheticModel = "<synthetic>"

// message is the usage one API message reported, which a transcript may
// repeat on several lines (Claude Code writes a line per content block of a
// message, each with the message's usage). The repeats are merged by taking
// the largest of each count, which is the same figure when they agree and the
// final one when a streamed message's usage grew.
type message struct {
	model      string
	in, out    int64
	cacheRead  int64
	cacheWrite int64 // all cache writes
	write1h    int64 // the part of cacheWrite that went to the 1 hour cache
	cost       *float64
}

func (m *message) merge(o message) {
	if m.model == "" {
		m.model = o.model
	}
	m.in, m.out = max(m.in, o.in), max(m.out, o.out)
	m.cacheRead, m.cacheWrite, m.write1h = max(m.cacheRead, o.cacheRead), max(m.cacheWrite, o.cacheWrite), max(m.write1h, o.write1h)
	if o.cost != nil && (m.cost == nil || *o.cost > *m.cost) {
		m.cost = o.cost
	}
}

// usd is the cost of m at rates r.
func (m message) usd(r Rates) float64 {
	w1h := min(m.write1h, m.cacheWrite)
	w5m := m.cacheWrite - w1h
	return (float64(m.in)*r.Input + float64(m.out)*r.Output + float64(m.cacheRead)*r.CacheRead +
		float64(w5m)*r.CacheWrite5m + float64(w1h)*r.CacheWrite1h) / 1e6
}

// transcriptLine is the part of a Claude Code transcript line this package
// reads. Every other key is ignored, so a transcript of another version still
// reads.
type transcriptLine struct {
	SessionID string   `json:"sessionId"`
	RequestID string   `json:"requestId"`
	CostUSD   *float64 `json:"costUSD"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheWrite    int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation *struct {
				Write1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// FromTranscript sums the usage of the Claude Code transcript at path, for
// session. The transcript of a session that was resumed begins with the lines
// of the session it was resumed from, which carry that session's id; a line
// with another session's id is that session's spend, counted by its own
// spend line, and is left out. A session id of "" counts every line. The
// transcripts of the session's subagents, in <path without .jsonl>/subagents,
// are counted with it: their usage is the session's, and the main transcript
// does not hold it.
//
// An empty path, or a transcript that is not there, is no spend. A line that
// is not JSON, or has no usage, is skipped. Prices may be nil.
func FromTranscript(path, session string, prices Prices) (Totals, error) {
	if path == "" {
		return Totals{}, nil
	}
	files := []string{path}
	subagents, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "*.jsonl"))
	slices.Sort(subagents)
	files = append(files, subagents...)

	byID := map[string]*message{}
	var anonymous []message
	for _, f := range files {
		if err := readUsage(f, session, byID, &anonymous); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Totals{}, err
		}
	}

	var t Totals
	models, unpriced := map[string]bool{}, map[string]bool{}
	count := func(m message) {
		if m.in+m.out+m.cacheRead+m.cacheWrite == 0 && m.cost == nil {
			return // a message that used nothing (Claude Code's own) is not spend
		}
		t.Messages++
		t.Input += m.in
		t.Output += m.out
		t.CacheRead += m.cacheRead
		t.CacheWrite += m.cacheWrite
		if m.model != "" && m.model != syntheticModel {
			models[m.model] = true
		}
		switch r, ok := prices.Lookup(m.model); {
		case m.cost != nil:
			t.USD += *m.cost
		case ok:
			t.USD += m.usd(r)
		default:
			unpriced[m.model] = true
		}
	}
	for _, m := range byID {
		count(*m)
	}
	for _, m := range anonymous {
		count(m)
	}
	t.USD = math.Round(t.USD*1e6) / 1e6
	t.Models, t.Unpriced = sortedKeys(models), sortedKeys(unpriced)
	return t, nil
}

// readUsage reads the usage records of one transcript file into byID (the
// messages that have an id, merged) and anonymous (those that have none).
func readUsage(path, session string, byID map[string]*message, anonymous *[]message) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	needle := []byte(`"usage"`)
	for {
		line, err := r.ReadBytes('\n')
		// Most lines are tool output and prompts: only a line that mentions
		// usage can hold a usage record, and skipping the others is what
		// keeps a transcript of many megabytes quick to read.
		if len(line) > 0 && bytes.Contains(line, needle) {
			var tl transcriptLine
			if json.Unmarshal(line, &tl) == nil {
				addLine(tl, session, byID, anonymous)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func addLine(tl transcriptLine, session string, byID map[string]*message, anonymous *[]message) {
	u := tl.Message.Usage
	if u == nil || (session != "" && tl.SessionID != "" && tl.SessionID != session) {
		return
	}
	m := message{model: tl.Message.Model, in: u.Input, out: u.Output, cacheRead: u.CacheRead, cacheWrite: u.CacheWrite, cost: tl.CostUSD}
	if u.CacheCreation != nil {
		m.write1h = u.CacheCreation.Write1h
	}
	id := tl.Message.ID
	if id == "" {
		id = tl.RequestID
	}
	if id == "" {
		*anonymous = append(*anonymous, m)
		return
	}
	if have, ok := byID[id]; ok {
		have.merge(m)
		return
	}
	byID[id] = &m
}

func sortedKeys(set map[string]bool) []string {
	var out []string
	for k := range set {
		if k != "" {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
