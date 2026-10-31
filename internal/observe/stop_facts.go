package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
)

// unattended reports whether nobody is watching the agent: it holds a claim
// made with --unattended that it has not released, or the project defaults to
// unattended (the unattended key of its jig.yaml marker). A claim is held by
// the agent that made it: a subagent shares its session's id, and its claim
// is no one else's.
func (h *handler) unattended() bool {
	if h.unattendedKnown {
		return h.unattendedVal
	}
	h.unattendedKnown = true
	h.unattendedVal = h.claimedUnattended() || h.markerUnattended()
	return h.unattendedVal
}

// claimedUnattended is true when the session's latest claim on the ticket, not
// released since, was made with --unattended by the agent of the event. A
// holder jig cannot tell (see claimHolder) leaves the claim the session's.
func (h *handler) claimedUnattended() bool {
	session := h.session()
	if session == "" || h.st == nil || h.w.Ticket == "" {
		return false
	}
	lines, err := h.journal()
	if err != nil {
		return false
	}
	var claim *journal.Line
	for i, l := range lines {
		if l.Session != session {
			continue
		}
		switch l.Event {
		case journal.EventClaim:
			claim = &lines[i]
		case journal.EventRelease:
			if claim != nil && claim.Slice == l.Slice {
				claim = nil
			}
		}
	}
	if claim == nil || !claim.Unattended {
		return false
	}
	holder, known := h.claimHolder(claim)
	return !known || holder == h.ev.Agent
}

// claimHolder is the agent that ran the `jig claim` call that made claim ("" is
// the session's own agent), and whether jig can tell. The claim line has the
// session and no agent: the CLI cannot know which agent it runs for. The
// hook that heard the call wrote the ledger a tool line with the agent on it,
// after the claim, and that is read back. A harness without hooks leaves none,
// and then nobody can say.
func (h *handler) claimHolder(claim *journal.Line) (agent string, known bool) {
	at, err := time.Parse(time.RFC3339Nano, claim.TS)
	if err != nil {
		return "", false
	}
	ticket := h.w.Ticket
	agent, known, err = ledger.AgentOf(h.d.JigHome, memoryWindow, h.session(), at, func(l journal.Line) bool {
		return ledger.RunsJig(l.Command, "", "claim") && !strings.Contains(l.Command, "--release") &&
			(l.Ticket == ticket || ledger.RunsJig(l.Command, ticket, "claim"))
	})
	h.fail(err)
	return agent, known
}

// markerUnattended reads the unattended default from the jig.yaml marker
// around the session: the nearest one above the directory, the repo or the
// project root.
func (h *handler) markerUnattended() bool {
	for _, dir := range []string{h.w.Cwd, h.w.RepoDir, h.w.Root} {
		if dir == "" {
			continue
		}
		if _, m, ok, err := project.FindMarker(dir); err == nil && ok {
			return m.Unattended
		}
	}
	return false
}

// verification is a verification of a commit for a slice that has no
// outcome yet.
type verification struct{ commit, slice string }

// inFlightWindow is how long a worker the ledger says was started is taken
// to be running when the journal has no outcome of it: a worker that died
// leaves that, and `jig verify --wait` on the commit runs it again.
const inFlightWindow = 10 * time.Minute

// pendingMaxAge is how old a verify-pending line may be and still count as a
// verification under way: an oracle is bound by the manifest timeout (30
// minutes unless it says otherwise), so a record older than this is a worker
// that died.
const pendingMaxAge = 2 * time.Hour

// inFlight lists the verifications of the ticket still running: those the
// journal shows pending, and those whose worker the ledger shows started
// (the machine started it a moment ago) before the worker wrote its first
// line.
func (h *handler) inFlight() []verification {
	lines, err := h.journal()
	if err != nil {
		h.fail(err)
		return nil
	}
	var out []verification
	oldest := h.d.Now().Add(-pendingMaxAge)
	for _, p := range journal.Pending(lines) {
		if at, err := time.Parse(time.RFC3339Nano, p.TS); err == nil && at.Before(oldest) {
			continue
		}
		out = append(out, verification{p.Commit, p.Slice})
	}
	cutoff := h.d.Now().Add(-inFlightWindow)
	h.fail(ledger.ReadBackward(h.d.JigHome, spawnWindow, func(l journal.Line) bool {
		if at, err := time.Parse(time.RFC3339Nano, l.TS); err == nil && at.Before(cutoff) {
			return false
		}
		if l.Event != journal.EventVerifySpawned || l.Ticket != h.w.Ticket || l.Commit == "" {
			return true
		}
		v := verification{l.Commit, l.Slice}
		if journal.VerificationState(lines, v.commit, v.slice).State == journal.VerifyNone && !slices.Contains(out, v) {
			out = append(out, v)
		}
		return true
	}))
	return out
}

// dirtyTracked lists the tracked files of the session directory that have
// uncommitted changes. It asks git without taking its optional locks, so it
// cannot get in the way of the agent next git command.
func (h *handler) dirtyTracked() []string {
	out, err := gitx.RunRaw(h.w.Cwd, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimRight(line, "\r"); len(line) > 3 {
			files = append(files, line[3:])
		}
	}
	return files
}

// fingerprint is a short stable name for a list of strings.
func fingerprint(parts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}
