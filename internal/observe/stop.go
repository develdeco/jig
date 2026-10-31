package observe

import (
	"fmt"
	"strings"
)

// finding is one thing outstanding when the agent is about to stop.
type finding struct {
	// ref fingerprints it: the same fact has the same ref at the next Stop.
	ref  string
	text string
	// hint is what to do about it, for a session that is held back.
	hint string
	// blocking findings hold an unattended session back; the rest are only
	// said.
	blocking bool
}

// findings collects what is outstanding on the located ticket: verifications
// still running, tracked files changed and not committed, questions still
// open, and the notes the session has not heard (an enforced note it was
// not yet refused a Stop for, whether or not it heard it).
func (h *handler) findings(mem sessionMemory) []finding {
	ticket := h.w.Ticket
	var out []finding
	for _, v := range h.inFlight() {
		sha := short12(v.commit)
		out = append(out, finding{
			ref:      "pending:" + sha + ":" + v.slice,
			text:     fmt.Sprintf("verification of %s for slice %s is still running", sha, v.slice),
			hint:     fmt.Sprintf("wait for it with `jig verify %s %s --slice %s --wait`", ticket, sha, v.slice),
			blocking: true,
		})
	}
	if files := h.dirtyTracked(); len(files) > 0 {
		out = append(out, finding{
			ref:      "dirty:" + fingerprint(files),
			text:     fmt.Sprintf("%d tracked %s changed and not committed: %s", len(files), plural(len(files), "file", "files"), listed(files, 5)),
			hint:     "commit them, since jig verifies commits and not the working tree",
			blocking: true,
		})
	}
	if questions, err := h.st.ReadQuestions(ticket); err != nil {
		h.fail(err)
	} else {
		for _, q := range questions {
			if q.Status == "open" {
				out = append(out, finding{ref: "question:" + q.ID, text: fmt.Sprintf("question %s is open (slice %s): %s", q.ID, q.Slice, oneLine(firstLine(q.Body), 100))})
			}
		}
	}
	for _, n := range h.collect(mem, "") {
		switch {
		case n.Enforce && !mem.blocked[n.Ref]:
			out = append(out, finding{ref: n.Ref, text: n.Text, blocking: true})
		case !mem.delivered[n.Ref]:
			out = append(out, finding{ref: n.Ref, text: n.Text})
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// listed names up to max of items, and how many more there are.
func listed(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:max], ", "), len(items)-max)
}

// stop says what is outstanding as the agent is about to stop. To a person
// at the keyboard it is a message, and the facts the model has not heard
// reach it at its next prompt or tool call. An unattended agent is held
// back, with the findings as the reason, when one of them is blocking and
// the agent is not already continuing for the same findings (it is held
// back once for each). A subagent's stop is its own: it names the ticket the
// subagent works on, and holds the subagent alone, never the session's own
// agent.
func (h *handler) stop() Result {
	located := h.locate()
	if h.ev.IsSubagent() && !(located && h.w.Ticket != "") {
		located = h.locateAgent() || located
	}
	if !located {
		return Result{}
	}
	mem := h.memory()
	if h.st == nil || h.w.Ticket == "" {
		h.hook("")
		return Result{}
	}
	fs := h.findings(mem)
	if len(fs) == 0 {
		h.hook("nothing outstanding")
		return Result{}
	}
	texts := make([]string, len(fs))
	for i, f := range fs {
		texts[i] = f.text
	}
	message := "jig: " + strings.Join(texts, "\njig: ")

	blocking, fresh := 0, 0
	for _, f := range fs {
		if f.blocking {
			blocking++
			if !mem.blocked[f.ref] {
				fresh++
			}
		}
	}
	if !h.unattended() || blocking == 0 || (h.ev.StopActive && fresh == 0) {
		h.hook("shown: " + strings.Join(texts, "; "))
		return Result{Message: message}
	}

	lines := make([]string, 0, len(fs))
	for _, f := range fs {
		line := f.text
		if f.hint != "" {
			line += ". " + capitalize(f.hint)
		}
		lines = append(lines, line)
		h.delivered(f.ref, f.text, outcomeStopBlock)
	}
	h.hook("blocked: " + strings.Join(texts, "; "))
	return Result{Message: message, Block: h.holdReason() + bullets(lines)}
}

// holdReason is what a refusal to stop says before the findings.
func (h *handler) holdReason() string {
	if h.ev.IsSubagent() {
		return "jig: this agent's claim is unattended, so it is held back from stopping until this is dealt with:\n"
	}
	return "jig: this session is unattended, so it is held back from stopping until this is dealt with:\n"
}

// locateAgent places a subagent where its own earlier lines in the ledger say
// it works, for a stop whose payload names a directory that is on no ticket
// (a harness that reports the session's directory for a subagent that works
// in a worktree of its own). It reports false, and changes nothing, when the
// ledger has no such line or the place is no better.
func (h *handler) locateAgent() bool {
	mem := h.memory()
	if mem.agentCwd == "" {
		return false
	}
	was, st, cfg := h.w, h.st, h.cfg
	if h.locateAt(mem.agentCwd) && h.w.Ticket != "" {
		return true
	}
	h.w, h.st, h.cfg = was, st, cfg
	return false
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
