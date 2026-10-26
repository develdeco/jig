package main

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// The words `jig gate` and `jig next` give for whether the calling session may
// review a ticket itself (verifydeliver.Freshness): it did not build it, it did,
// or jig cannot tell.
const (
	freshYes     = "yes"
	freshNo      = "no"
	freshUnknown = "unknown"
)

// reviewAdvice is what to tell the calling session about who reviews a ticket:
// whether the session is fresh for it, and the lines that say what to do.
type reviewAdvice struct {
	State string
	Lines []string
}

// sessionAdvice reads the freshness of the session the environment names
// (envSession) for ticket and gives the advice for it. ok is false when the
// environment names no session, as when the TUI's `g` opens the round: there is
// then nobody to advise. A session whose record cannot be read is one jig cannot
// tell, which gets the advice for a session that may have built the ticket.
func sessionAdvice(e env, d verifydeliver.Deps, ticket string) (reviewAdvice, bool) {
	session := envSession(e)
	if session == "" {
		return reviewAdvice{}, false
	}
	f, err := verifydeliver.Freshness(d, ticket, session)
	if err != nil {
		f = verifydeliver.Fresh{}
	}
	clone, _ := e.getwd()
	return adviceFor(f, ticket, clone), true
}

// adviceFor is the advice for a session with freshness f. A fresh session
// reviews in its own context. One that is not, or that jig cannot tell, has a
// subagent review: the line that says why, how to start the subagent in Claude
// Code, the prompt that makes it a reviewer, and why the main session must not
// read the round as well.
func adviceFor(f verifydeliver.Fresh, ticket, clone string) reviewAdvice {
	if f.Known && f.Fresh {
		return reviewAdvice{State: freshYes, Lines: []string{
			fmt.Sprintf("Nothing in this session built %s, so review it in this session: run the review skill (`jig skill review`)", ticket),
		}}
	}
	state, first := freshNo, fmt.Sprintf("This session %s: spawn a subagent to review %s", whyWords(f.Why), ticket)
	if !f.Known {
		state = freshUnknown
		first = fmt.Sprintf("jig cannot tell whether this session built %s (it has no record of the session): spawn a subagent to review it", ticket)
	}
	return reviewAdvice{State: state, Lines: []string{
		first,
		"In Claude Code, call the Agent tool (Task in older versions) with `subagent_type: general-purpose`, `model: opus` unless the person named another model, and no worktree isolation (the round has its own); another harness starts a fresh session instead. Claude Code may run it in the background whatever you ask: wait for its verdict with TaskOutput (`block: true`, the agent id the call returned), or, in an attended session only, end the turn and go on when the harness says it finished; an unattended session ends with its turn, so it must wait in it. Never call another tool to stall. Pass it this prompt",
		reviewerPrompt(ticket, clone),
		"The main session must not run `jig review context` itself: jig sees a session and its subagents as one session, so if both read the round it cannot tell which of them closed it, and the round is not independent",
	}}
}

// reviewerPrompt is the self-contained prompt of the subagent that reviews
// ticket in clone.
func reviewerPrompt(ticket, clone string) string {
	where := "your working directory"
	if clone != "" {
		where = clone
	}
	return fmt.Sprintf("\"You did not build %s. In %s, run the review skill (`jig skill review`): `jig review context %s --model <your model id>`, then `jig finding` for each problem, then `jig review done %s`. You are the reviewer: do not spawn a subagent. Reply with the verdict line only.\"",
		ticket, where, ticket, ticket)
}

// whyWords joins what makes a session not fresh into a clause for a sentence:
// the first three, and how many more there are.
func whyWords(why []string) string {
	const shown = 3
	if len(why) == 0 {
		return "has worked on it"
	}
	words := strings.Join(why[:min(len(why), shown)], ", ")
	if more := len(why) - shown; more > 0 {
		words += fmt.Sprintf(" (and %d more)", more)
	}
	return words
}

// reviewNoteText is the next-phase note an unattended session gets at Stop when
// every slice of ticket is green and no round has been opened: open the gate,
// and who reviews. The lines `jig gate` prints carry the Agent call and the
// prompt, so the note points at them instead of repeating them.
func reviewNoteText(f verifydeliver.Fresh, ticket string) string {
	head := fmt.Sprintf("%s is built, every slice green, and no review round is open: open the gate and review", ticket)
	if f.Known && f.Fresh {
		return fmt.Sprintf("%s: run `jig gate %s`, then review it in this session with the review skill (`jig skill review`)", head, ticket)
	}
	why := "jig cannot tell whether this session built it"
	if f.Known {
		why = "this session " + whyWords(f.Why)
	}
	return fmt.Sprintf("%s: run `jig gate %s`, then have a subagent review it, not this session (%s); `jig gate` gives the Agent call and the prompt to pass", head, ticket, why)
}
