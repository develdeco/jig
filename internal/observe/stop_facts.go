package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/verify"
)

// unattended reports whether nobody is watching the agent: it holds a claim
// made with --unattended that it has not released, or the project defaults to
// unattended (the unattended key of its jig.yaml marker). A claim is held by
// the agent that made it: a subagent shares its session's id, and its claim
// is no one else's, while each agent of a session holds its own.
func (h *handler) unattended() bool {
	if h.unattendedKnown {
		return h.unattendedVal
	}
	h.unattendedKnown = true
	h.unattendedVal = h.claimedUnattended() || h.markerUnattended()
	return h.unattendedVal
}

// claimedUnattended is true when the agent of the event holds a claim on the
// ticket made with --unattended: the agent's own latest claim of the session,
// not released since. The holding is per agent, because a chart run's agents
// (a ticket subagent and the repo builders nested in it) share one session id
// and claim slices of the same ticket at once; a release names a slice and
// clears the claim of that slice, whoever holds it. A latest claim whose
// holder jig cannot tell (see claimHolder) is the session's: it holds every
// agent of the session, as it did before the holding was per agent.
func (h *handler) claimedUnattended() bool {
	session := h.session()
	if session == "" || h.st == nil || h.w.Ticket == "" {
		return false
	}
	lines, err := h.journal()
	if err != nil {
		return false
	}
	var held []*journal.Line // the session's claims not released since, oldest first
	for i, l := range lines {
		if l.Session != session {
			continue
		}
		switch l.Event {
		case journal.EventClaim:
			held = append(held, &lines[i])
		case journal.EventRelease:
			held = slices.DeleteFunc(held, func(c *journal.Line) bool { return c.Slice == l.Slice })
		}
	}
	if len(held) == 0 {
		return false
	}
	latest := held[len(held)-1]
	if _, known := h.claimHolder(latest); !known {
		return latest.Unattended
	}
	for i := len(held) - 1; i >= 0; i-- {
		if holder, known := h.claimHolder(held[i]); known && holder == h.ev.Agent {
			return held[i].Unattended
		}
	}
	return false
}

// claimHolder is the agent that ran the `jig claim` call that made claim ("" is
// the session's own agent), and whether jig can tell. The claim line has the
// session and no agent: the CLI cannot know which agent it runs for. The
// hook that heard the call wrote the ledger a tool line with the agent on it,
// after the claim, and that is read back, among the lines of the claim's own
// slice: agents of one session claim other slices of the ticket at the same
// moment, and their lines must not blur this one's. A harness without hooks
// leaves none, and then nobody can say.
func (h *handler) claimHolder(claim *journal.Line) (agent string, known bool) {
	at, err := time.Parse(time.RFC3339Nano, claim.TS)
	if err != nil {
		return "", false
	}
	ticket := h.w.Ticket
	agent, known, err = ledger.AgentOf(h.d.JigHome, memoryWindow, h.session(), at, func(l journal.Line) bool {
		return ledger.RunsJig(l.Command, "", "claim") && !strings.Contains(l.Command, "--release") &&
			(l.Ticket == ticket || ledger.RunsJig(l.Command, ticket, "claim")) &&
			claimsSlice(l.Command, claim.Slice)
	})
	h.fail(err)
	return agent, known
}

// claimsSlice reports whether the shell command line cmd can be the claim of
// slice: it names no --slice (the environment gives it), or names that one.
func claimsSlice(cmd, slice string) bool {
	if slice == "" {
		return true
	}
	words := strings.Fields(cmd)
	named := false
	for i := 0; i < len(words); i++ {
		flag, value, hasValue := strings.Cut(words[i], "=")
		if flag != "--slice" && flag != "-slice" {
			continue
		}
		if !hasValue && i+1 < len(words) {
			i++
			value = words[i]
		}
		named = true
		if strings.Trim(value, `"'`) == slice {
			return true
		}
	}
	return !named
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

// inFlightWindow is how long a worker the ledger says was started is taken
// to be running when the journal has no outcome of it: the worker has not
// yet written its first line.
const inFlightWindow = 10 * time.Minute

// verificationFindings tells the state of the ticket's verifications as the
// agent is about to stop. A verification under way (a pending line whose
// worker is alive, or a worker the ledger started a moment ago that has not
// yet written to the journal) holds an unattended session as it always did. A
// commit that is not verified, or whose verification is stale, holds it too,
// since no one is judging it and the clearing command runs the oracle. A
// failed, stopped or timed-out verification is told and holds nobody back:
// the builder may stop, and the slice stays building.
func (h *handler) verificationFindings() []finding {
	lines, err := h.journal()
	if err != nil {
		h.fail(err)
		return nil
	}
	truth := h.verifyTruth(lines)
	rows := truth.Derive()

	var out []finding
	underWay := map[phase.CommitRef]bool{}
	for _, r := range rows {
		if r.State == phase.VerifyRunning {
			underWay[phase.CommitRef{SHA: r.Commit, Slice: r.Slice}] = true
			out = append(out, h.runningFinding(r.Commit, r.Slice))
		}
	}
	cutoff := h.d.Now().Add(-inFlightWindow)
	h.fail(ledger.ReadBackward(h.d.JigHome, spawnWindow, func(l journal.Line) bool {
		if at, err := time.Parse(time.RFC3339Nano, l.TS); err == nil && at.Before(cutoff) {
			return false
		}
		if l.Event != journal.EventVerifySpawned || l.Ticket != h.w.Ticket || l.Commit == "" {
			return true
		}
		p := phase.CommitRef{SHA: l.Commit, Slice: l.Slice}
		if journal.VerificationState(lines, l.Commit, l.Slice).State == journal.VerifyNone && !underWay[p] {
			underWay[p] = true
			out = append(out, h.runningFinding(l.Commit, l.Slice))
		}
		return true
	}))

	for _, r := range rows {
		if underWay[phase.CommitRef{SHA: r.Commit, Slice: r.Slice}] || r.State == phase.VerifyRunning || !r.State.Unclear() {
			continue
		}
		sha := short12(r.Commit)
		f := finding{ref: string(r.State) + ":" + sha + ":" + r.Slice, blocking: r.State.Holds()}
		switch r.State {
		case phase.VerifyNotVerified:
			f.text = fmt.Sprintf("commit %s for slice %s is not verified", sha, r.Slice)
			f.hint = fmt.Sprintf("verify it with `%s`", r.Clear)
		case phase.VerifyStale:
			f.text = fmt.Sprintf("verification of %s for slice %s is stale: its worker is gone", sha, r.Slice)
			f.hint = fmt.Sprintf("run it again with `%s`", r.Clear)
		case phase.VerifyFailed:
			f.text = fmt.Sprintf("verification of %s for slice %s failed: %s", sha, r.Slice, r.FailLine)
		default:
			f.text = fmt.Sprintf("verification of %s for slice %s %s; run it again with `%s`", sha, r.Slice, r.State.Told(), r.Clear)
		}
		out = append(out, f)
	}
	return out
}

func (h *handler) runningFinding(commit, slice string) finding {
	sha := short12(commit)
	return finding{
		ref:      "pending:" + sha + ":" + slice,
		text:     fmt.Sprintf("verification of %s for slice %s is still running", sha, slice),
		hint:     fmt.Sprintf("wait for it with `jig verify %s %s --slice %s --wait`", h.w.Ticket, sha, slice),
		blocking: true,
	}
}

// verifyTruth gathers what the derivation of the ticket's verifications reads.
// The branch is read, with one git log, only when the ticket was found by the
// branch the work is on and has a start sha; the commits are then those of that
// branch.
func (h *handler) verifyTruth(lines []journal.Line) verify.Truth {
	t := verify.Truth{JigHome: h.d.JigHome, Ticket: h.w.Ticket, Lines: lines, Now: h.d.Now()}
	if sl, err := h.st.ReadSlices(h.w.Ticket); err == nil {
		for _, s := range sl {
			t.Slices = append(t.Slices, s.ID)
		}
	}
	if h.w.TicketHow == locate.HowBranch || h.w.TicketHow == locate.HowBranchAlias {
		dir := h.w.Root
		if dir == "" {
			dir = h.w.RepoDir
		}
		if start := verify.StartSHA(h.st, h.w.Ticket, h.w.Repo); start != "" && dir != "" {
			if c, err := verify.BranchCommits(dir, start, "HEAD"); err == nil {
				t.Commits, t.BranchKnown = c, true
			}
		}
	}
	return t
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
