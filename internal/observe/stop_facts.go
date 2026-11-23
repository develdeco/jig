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
	return h.claimHolderOn(h.w.Ticket, claim)
}

// claimHolderOn is claimHolder for a claim on ticket.
func (h *handler) claimHolderOn(ticket string, claim *journal.Line) (agent string, known bool) {
	at, err := time.Parse(time.RFC3339Nano, claim.TS)
	if err != nil {
		return "", false
	}
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

// verificationFindings tells the state of the ticket's verifications as the
// agent is about to stop. A commit with no recorded result holds an
// unattended session, since no run is recorded for it and the clearing command
// runs the oracle. A failed, stopped, timed-out or could-not-run verification
// is told and holds nobody back: the builder may stop, and the slice stays
// building. An old verify-pending line is no result and is neither held on nor
// said.
func (h *handler) verificationFindings() []finding {
	lines, err := h.journal()
	if err != nil {
		h.fail(err)
		return nil
	}
	truth := h.verifyTruth(lines)
	// Only a slice's building tip is held to a result: an older commit of the
	// slice, or one it has a verified line at or after, is history
	// (Truth.Outstanding), the rule `jig here` and `jig status` share.
	rows := truth.Outstanding()

	var out []finding
	for _, r := range rows {
		if !r.State.Unclear() {
			continue
		}
		if !r.State.Holds() && !h.building(r.Slice) {
			// A failed or stopped run is told to a builder still on the slice;
			// once the slice is green, stalled or parked it is history.
			continue
		}
		sha := short12(r.Commit)
		f := finding{ref: r.Told() + ":" + sha + ":" + r.Slice, blocking: r.State.Holds()}
		switch r.State {
		case phase.VerifyNoResult:
			f.text = fmt.Sprintf("commit %s for slice %s has no recorded result", sha, r.Slice)
			f.hint = fmt.Sprintf("verify it with `jig verify %s --slice %s`", h.w.Ticket, r.Slice)
		case phase.VerifyFailed:
			if r.Cause != "" {
				f.text = fmt.Sprintf("verification of %s for slice %s %s; run it again with `%s`", sha, r.Slice, r.Cause, r.Clear)
			} else {
				f.text = fmt.Sprintf("verification of %s for slice %s failed: %s", sha, r.Slice, r.FailLine)
			}
		default:
			f.text = fmt.Sprintf("verification of %s for slice %s could not run: %s; run it again with `%s`", sha, r.Slice, r.Error, r.Clear)
		}
		out = append(out, f)
	}
	return out
}

// verifyTruth gathers what the derivation of the ticket's verifications reads.
// The branch is read, with one git log, only when the ticket was found by the
// branch the work is on and has a start sha; the commits are then those of that
// branch.
func (h *handler) verifyTruth(lines []journal.Line) verify.Truth {
	t := verify.Truth{Ticket: h.w.Ticket, Lines: lines}
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
		repo, target := h.repoAndTarget()
		start := verify.StartSHA(h.st, h.w.Ticket, repo)
		if start == "" && dir != "" && target != "" {
			// A branch no claim started from (the work was begun by hand) has
			// no recorded start: its commits are those past the target branch,
			// as origin has it, else as the clone has it.
			for _, ref := range []string{"refs/remotes/origin/" + target, "refs/heads/" + target} {
				if base, err := gitx.MergeBase(dir, "HEAD", ref); err == nil && base != "" {
					start = base
					break
				}
			}
		}
		if start != "" && dir != "" {
			if c, parents, err := verify.BranchCommits(dir, start, "HEAD"); err == nil {
				t.Commits, t.Parents, t.BranchKnown = c, parents, true
			}
		}
	}
	return t
}

// repoAndTarget is the located repo name in the project and its target
// branch. A repo the project does not list has no target (a commit with no
// known fork point is not held to one); the project's only repo is the repo
// of a Locate that named none.
func (h *handler) repoAndTarget() (name, target string) {
	for _, r := range h.cfg.Repos {
		if h.w.Repo != "" && r.Name() == h.w.Repo {
			return r.Name(), r.TargetBranch()
		}
	}
	if len(h.cfg.Repos) == 1 {
		return h.cfg.Repos[0].Name(), h.cfg.Repos[0].TargetBranch()
	}
	return h.w.Repo, ""
}

// dirtyTracked lists the tracked files of the session directory that have
// uncommitted changes. It asks git without taking its optional locks, so it
// cannot get in the way of the agent next git command.
func (h *handler) dirtyTracked() []string {
	if h.noDir {
		return nil
	}
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

// building reports whether the slice is being built now.
func (h *handler) building(slice string) bool {
	ss, err := h.st.ReadSliceState(h.w.Ticket, slice)
	return err == nil && ss.State == "building"
}
