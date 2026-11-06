package observe

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// A prompt that names a ticket of the store hears where that ticket is: its
// phase and state, the skill for the phase, and where to ask what it needs. The
// ticket's own phase says which skill applies, so the prompt is never read for
// intent: only for tokens shaped like one of the store's declared keys and a
// number (JIG-12), and only the ones that name a ticket the store holds.

// maxNamedTickets is how many tickets one prompt is answered for.
const maxNamedTickets = 3

// phaseSkill is the skill for each phase of a ticket (`jig skill <name>`).
var phaseSkill = map[phase.Name]string{
	phase.Brief:   "intake",
	phase.Build:   "build",
	phase.Review:  "review",
	phase.Publish: "publish",
}

// namedTickets is the tickets of the store the event's prompt names, in the
// order named, each once, at most maxNamedTickets. A token counts when its key
// is one the project declares (any case) and the store holds a ticket under
// the id; an id the store does not hold names nothing.
func (h *handler) namedTickets() []string {
	if h.ev.Kind != KindPrompt || h.ev.Prompt == "" || h.st == nil || len(h.cfg.Keys) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h.cfg.Keys))
	for k := range h.cfg.Keys {
		keys = append(keys, regexp.QuoteMeta(k))
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	re, err := regexp.Compile(`(?i)(?:^|[^A-Za-z0-9_])(` + strings.Join(keys, "|") + `)-([0-9]+)\b`)
	if err != nil {
		h.fail(fmt.Errorf("observe: the declared keys make no pattern: %w", err))
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(h.ev.Prompt, -1) {
		id := strings.ToUpper(m[1]) + "-" + m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		if resolved, err := h.st.ResolveTicket(id); err == nil {
			id = resolved
		}
		if !h.st.TicketExists(id) {
			continue
		}
		out = append(out, id)
		if len(out) == maxNamedTickets {
			break
		}
	}
	return out
}

// phaseNotes is the note for each of the named tickets that is not done, once
// for each ticket and phase (the Ref is phase:<ticket>:<phase>). A ticket
// whose phase cannot be derived costs its own note.
func (h *handler) phaseNotes(tickets []string) []Note {
	if h.d.Derive == nil {
		return nil
	}
	var out []Note
	for _, id := range tickets {
		t, err := h.d.Derive(h.st, id)
		if err != nil {
			h.fail(fmt.Errorf("phase of %s: %w", id, err))
			continue
		}
		if t.Code == phase.CodeDone || t.Code == phase.CodeClosed {
			continue
		}
		state := "ready"
		if !t.Ready {
			state = "not ready"
			if t.Reason != "" {
				state += " (" + oneLine(t.Reason, 200) + ")"
			}
		}
		out = append(out, Note{
			Ref: fmt.Sprintf("phase:%s:%s", id, t.Current),
			Text: fmt.Sprintf("%s is at its %s phase, %s: use the %s skill (`jig skill %s`); `jig next %s` says what the phase needs.",
				id, t.Current, state, phaseSkill[t.Current], phaseSkill[t.Current], id),
		})
	}
	return out
}
