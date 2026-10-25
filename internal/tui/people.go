package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// Who did what is said in roles, not ids: a session that claimed a slice is
// the builder, one that closed a gate round the reviewer, one that recorded
// the brief's approval the planner, and a line a person wrote at a terminal
// is "you". A session's harness and model come from its session-start line
// in the ledger, whatever ticket it named then.

// sessionInfo is what the ledger said of a session when it started.
type sessionInfo struct {
	Harness, Model string
}

// The roles a session can play on a ticket, in the order they are named.
const (
	rolePlanner  = "planner"
	roleBuilder  = "builder"
	roleReviewer = "reviewer"
)

// roleOf is the role a journal line shows its session playing, or "".
func roleOf(event string) string {
	switch event {
	case journal.EventApproval, journal.EventRevalidate:
		return rolePlanner
	case journal.EventClaim, journal.EventRelease, journal.EventVerifyPending, journal.EventQuestion,
		journal.EventDispatch, journal.EventResult:
		return roleBuilder
	case journal.EventGateOpen, journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean:
		return roleReviewer
	}
	return ""
}

// roleSessions are, for each role, the sessions that played it on the
// ticket, in the order they first did. One session can play several roles: a
// session that drafts the brief and then builds it is the planner and the
// builder.
func (t *ticket) roleSessions() map[string][]string {
	out := map[string][]string{}
	seen := map[[2]string]bool{}
	for _, l := range t.lines {
		r := roleOf(l.Event)
		if l.Session == "" || r == "" || seen[[2]string{r, l.Session}] {
			continue
		}
		seen[[2]string{r, l.Session}] = true
		out[r] = append(out[r], l.Session)
	}
	return out
}

// roleLabel is the name of a session in a role: the role, numbered when more
// than one session played it ("builder 2"), or "" when it never did.
func (t *ticket) roleLabel(session, role string) string {
	sessions := t.roleSessions()[role]
	for i, s := range sessions {
		if s == session {
			if len(sessions) > 1 {
				return role + " " + strconv.Itoa(i+1)
			}
			return role
		}
	}
	return ""
}

// firstRole is the role a session played first on the ticket, or "".
func (t *ticket) firstRole(session string) string {
	for _, l := range t.lines {
		if l.Session == session {
			if r := roleOf(l.Event); r != "" {
				return r
			}
		}
	}
	return ""
}

// whoAs names a session in role, else by the role it played first, else as
// "session" and the start of its id; "" for a line that names no session.
func (m *Model) whoAs(t *ticket, session, role string) string {
	if session == "" {
		return ""
	}
	if role != "" {
		if l := t.roleLabel(session, role); l != "" {
			return l
		}
	}
	if r := t.firstRole(session); r != "" {
		return t.roleLabel(session, r)
	}
	return "session " + shortID(session)
}

// who names a session by the role it played first on the ticket.
func (m *Model) who(t *ticket, session string) string { return m.whoAs(t, session, "") }

// whoWith is who, with the harness and model the session runs when the
// ledger said: "reviewer (Claude Code · Opus 5.5)".
func (m *Model) whoWith(t *ticket, session string) string { return m.whoWithAs(t, session, "") }

// whoWithAs is whoWith for a session playing role in what is worded: a
// session that planned and then claims a slice is named as its builder.
func (m *Model) whoWithAs(t *ticket, session, role string) string {
	name := m.whoAs(t, session, role)
	if name == "" {
		return ""
	}
	if d := m.sessionDesc(session); d != "" {
		return name + " (" + d + ")"
	}
	return name
}

// sessionDesc is a session's harness and model in words, or "".
func (m *Model) sessionDesc(session string) string {
	info, ok := m.w.sessionInfo[session]
	if !ok {
		return ""
	}
	return harnessAndModel(info.Harness, info.Model)
}

// harnessAndModel is a harness and a model in words, "Claude Code · Opus 5.5".
func harnessAndModel(harness, model string) string {
	var parts []string
	if h := harnessName(harness); h != "" {
		parts = append(parts, h)
	}
	if mo := modelName(model); mo != "" {
		parts = append(parts, mo)
	}
	return strings.Join(parts, " · ")
}

// played is whether session played role on the ticket.
func (t *ticket) played(session, role string) bool {
	return slices.Contains(t.roleSessions()[role], session)
}

// reviewerName is how a session that reviewed the ticket is named: a reviewer
// subagent when it also played the planner or the builder, since a subagent
// shares its parent's session id and the first role that id played is not the
// one it reviews in; else the reviewer, numbered when several sessions
// reviewed. "" for a session that played none of the roles.
func (t *ticket) reviewerName(session string) string {
	if t.played(session, rolePlanner) || t.played(session, roleBuilder) {
		return "reviewer subagent"
	}
	return t.roleLabel(session, roleReviewer)
}

// roundModel is the model that reviewed round n, as the round's own lines say
// it (the gate-reviewer line carries what the reviewer ran on, and so do the
// lines that close the round): the latest of them that names one, else "".
func (t *ticket) roundModel(n int) string {
	model := ""
	for _, l := range t.lines {
		switch l.Event {
		case journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean:
			if l.Model != "" && t.roundOf(l) == n {
				model = l.Model
			}
		}
	}
	return model
}

// reviewerDesc is the harness and model round n's reviewer ran on, in words.
// The model is the one the line being worded names (own), else the round's in
// its lines, else, when the caller has the status's row for the round (rec),
// that one, since a reviewer subagent runs on a model of its own; the model the
// session started with is the last resort. The harness is the session's, else
// the row's.
func (m *Model) reviewerDesc(t *ticket, session string, n int, own string, rec *statusRound) string {
	info := m.w.sessionInfo[session]
	harness, model := info.Harness, info.Model
	if rec != nil {
		harness, model = firstOf(harness, rec.Harness), firstOf(rec.Model, model)
	}
	return harnessAndModel(harness, firstOf(own, t.roundModel(n), model))
}

// reviewerRole names a session that reviewed by its role alone: "reviewer
// subagent". "" for a line that names no session.
func (m *Model) reviewerRole(t *ticket, session string) string {
	if session == "" {
		return ""
	}
	return firstOf(t.reviewerName(session), m.who(t, session))
}

// reviewerWho names the session that reviewed round n with its harness and the
// model it ran on: "reviewer subagent (Claude Code · Opus 5)". own is the model
// the line being worded names, "" for none, and rec the status's row for the
// round, nil for none. "" for a line that names no session.
func (m *Model) reviewerWho(t *ticket, session string, n int, own string, rec *statusRound) string {
	name := m.reviewerRole(t, session)
	if name == "" {
		return ""
	}
	if d := m.reviewerDesc(t, session, n, own, rec); d != "" {
		return name + " (" + d + ")"
	}
	return name
}

// byWhom is the person a line's provenance names: "you" for a person at a
// terminal, the session's role for an agent, else "".
func (m *Model) byWhom(t *ticket, provenance, session string) string {
	if provenance == "person" {
		return "you"
	}
	return m.who(t, session)
}

// shortID is the first eight characters of an id.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// harnessName is a harness as people call it.
func harnessName(h string) string {
	switch h {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini CLI"
	case "cursor":
		return "Cursor"
	case "copilot":
		return "GitHub Copilot"
	case "opencode":
		return "OpenCode"
	case "", "neutral", "git":
		return ""
	}
	return h
}

// modelName is a model id as people call it: claude-sonnet-5 is "Sonnet 5",
// claude-opus-5-5 is "Opus 5.5". An id of another shape is kept as it is.
func modelName(id string) string {
	raw := id
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "claude-")
	var words, nums []string
parts:
	for _, p := range strings.Split(id, "-") {
		switch {
		case p == "":
		case isDigits(p) && len(p) >= 8: // a date suffix
			break parts
		case isDigits(p):
			nums = append(nums, p)
		case len(nums) == 0:
			words = append(words, strings.ToUpper(p[:1])+p[1:])
		default:
			break parts
		}
	}
	if len(words) == 0 {
		return raw
	}
	out := strings.Join(words, " ")
	if len(nums) > 0 {
		out += " " + strings.Join(nums, ".")
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// personName is the name in a "Name <email>" identity.
func personName(identity string) string {
	if i := strings.Index(identity, " <"); i > 0 {
		return identity[:i]
	}
	return identity
}
