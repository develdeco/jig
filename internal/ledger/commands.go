package ledger

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// What a jig command does is written to the ticket's journal with the session
// that ran it and nothing more: the CLI cannot know which agent of the session
// it is running for. The harness's hook knows, and writes the ledger a tool
// line for the shell call with its agent on it. So who ran a jig command is
// read back from those lines: RunsJig tells which tool lines ran the command,
// AgentOf which agent they name.

// RunsJig reports whether the shell command line cmd runs `jig <verb...>` for
// ticket: jig (any path, with or without .exe), then the verb words in order,
// then the ticket among the words up to the next shell separator, or no
// ticket word at all (the verb then takes the ticket from the branch the
// clone is on). An empty ticket matches any.
func RunsJig(cmd, ticket string, verb ...string) bool {
	words := strings.Fields(cmd)
	for i, w := range words {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/")))
		if base != "jig" && base != "jig.exe" {
			continue
		}
		if i+len(verb) >= len(words) || !verbAt(words[i+1:], verb) {
			continue
		}
		if ticket == "" {
			return true
		}
		positional := false
		for _, a := range words[i+1+len(verb):] {
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

// verbAt reports whether words begin with the verb words.
func verbAt(words, verb []string) bool {
	if len(words) < len(verb) {
		return false
	}
	for i, v := range verb {
		if words[i] != v {
			return false
		}
	}
	return true
}

// agentSlack is how far before since a line's stamp may be and still be
// read: lines are stamped before the append lock is taken, so two writers can
// land a little out of order.
const agentSlack = time.Minute

// AgentOf is the agent the machine ledger names for a command of session: the
// agent on the tool lines of the session, stamped at or after since, that
// match accepts, when they all name the same one ("" is the session's own
// agent). Lines naming two agents say nothing of which ran the command, and
// no line says nothing: ok is false. It reads the ledger from its end and
// stops at the first line older than since, so the cost is the distance from
// the end back to since; maxBytes bounds it as in ReadBackward (zero or less
// reads as far back as since).
func AgentOf(root string, maxBytes int64, session string, since time.Time, match func(journal.Line) bool) (agent string, ok bool, err error) {
	if session == "" {
		return "", false, nil
	}
	ambiguous := false
	err = ReadBackward(root, maxBytes, func(l journal.Line) bool {
		at, perr := time.Parse(time.RFC3339Nano, l.TS)
		if perr != nil {
			return true
		}
		if at.Before(since.Add(-agentSlack)) {
			return false
		}
		if at.Before(since) || l.Event != journal.EventTool || l.Session != session || !match(l) {
			return true
		}
		if ok && l.Agent != agent {
			ambiguous = true
			return false
		}
		agent, ok = l.Agent, true
		return true
	})
	if err != nil || ambiguous {
		return "", false, err
	}
	return agent, ok, nil
}
