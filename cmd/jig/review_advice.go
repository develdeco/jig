package main

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/locate"
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
// read the round as well. A session that built the ticket and whose tool calls
// reach jig with no agent id (no jig hooks) has a separate session review
// instead (separateSessionAdvice): a subagent of it would review as the
// session itself. A session jig has no record of keeps the subagent: none of
// the builders is that session, so neither is its subagent.
func adviceFor(f verifydeliver.Fresh, ticket, clone string) reviewAdvice {
	if f.Known && f.Fresh {
		return reviewAdvice{State: freshYes, Lines: []string{
			fmt.Sprintf("Nothing in this session built %s, so review it in this session: run the review skill (`jig skill review`)", ticket),
		}}
	}
	if f.Known && !f.AgentIDs {
		return separateSessionAdvice(f, ticket, clone)
	}
	state, first := freshNo, fmt.Sprintf("This session %s: spawn a subagent to review %s", whyWords(f.Why), ticket)
	if !f.Known {
		state = freshUnknown
		first = fmt.Sprintf("jig cannot tell whether this session built %s (it has no record of the session): spawn a subagent to review it", ticket)
	}
	return reviewAdvice{State: state, Lines: []string{
		first,
		"In Claude Code, call the Agent tool (Task in older versions) with `subagent_type: general-purpose`, `model: opus` unless the person named another model, and no worktree isolation (the round has its own); another harness starts a fresh session instead. Claude Code runs it in the background whatever you ask: wait for its verdict in this turn with TaskOutput (`block: true`, the agent id the call returned; load the tool with ToolSearch when your tools do not list it). Do not end the turn or schedule a wakeup while it runs, and never call another tool to stall. Pass it this prompt",
		reviewerPrompt(ticket, clone),
		"The main session must not run `jig review context` itself: jig sees a session and its subagents as one session, so if both read the round it cannot tell which of them closed it, and the round is not independent",
	}}
}

// separateSessionAdvice is the advice for a session that built ticket and
// whose subagents jig cannot tell from it: start the reviewer as a headless
// session of its own, with the command that does it in Claude Code
// (reviewerCommand), and never review in this session or a subagent of it.
func separateSessionAdvice(f verifydeliver.Fresh, ticket, clone string) reviewAdvice {
	return reviewAdvice{State: freshNo, Lines: []string{
		fmt.Sprintf("This session %s, and jig has no hook record of its tool calls, so a subagent's review would be recorded as this session's: start a separate session to review %s", whyWords(f.Why), ticket),
		"In Claude Code, run this command with the Bash tool in the background (`run_in_background`: a review outlasts a shell call's time limit), with `--model opus` unless the person named another model; another harness starts a fresh session of its own with the same prompt. The command unsets the variables jig reads a session from, so the new session reviews under an id of its own, and allows every tool, since it cannot ask. Wait for it in this turn with TaskOutput (`block: true`, the task id the call returned) where your tools have it, else at the notice that it exited, then relay the verdict line it prints",
		reviewerCommand(ticket, clone),
		"Do not review it in this session or a subagent of it: jig records whoever closes the round with `jig review done`, and here that is this session, which built it",
	}}
}

// reviewerCommand is the sh command line that starts the reviewer of ticket
// in clone as a headless Claude Code session: with each variable jig reads a
// session from unset, since a nested claude gives its tools a session id of
// its own but keeps $JIG_SESSION, which jig reads first, and its input from
// /dev/null, since claude -p otherwise waits for input on a shell tool's
// stdin and warns.
func reviewerCommand(ticket, clone string) string {
	words := []string{"env"}
	for _, name := range append([]string{locate.SessionEnv}, locate.HarnessSessionEnv...) {
		words = append(words, "-u", name)
	}
	words = append(words, "claude", "-p", shQuote(reviewerPromptText(ticket, clone)), "--model", "opus", "--permission-mode", "bypassPermissions", "<", "/dev/null")
	return strings.Join(words, " ")
}

// shQuote quotes s as one word for a POSIX shell: in single quotes, each
// single quote of s closed, escaped and reopened.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// reviewerPrompt is the self-contained prompt of the subagent that reviews
// ticket in clone, in double quotes as the advice shows it.
func reviewerPrompt(ticket, clone string) string {
	return `"` + reviewerPromptText(ticket, clone) + `"`
}

// reviewerPromptText is the prompt that makes an agent, a subagent or a
// session of its own, the reviewer of ticket in clone.
func reviewerPromptText(ticket, clone string) string {
	where := "your working directory"
	if clone != "" {
		where = clone
	}
	return fmt.Sprintf("You did not build %s. In %s, run the review skill (`jig skill review`): `jig review context %s --model <your model id>`, then `jig finding` for each problem, then `jig review done %s`. You are the reviewer: do not have a subagent or another session review it. Reply with the verdict line only.",
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
// and who reviews, as adviceFor tells it. The lines `jig gate` prints carry the
// Agent call or the command, and the prompt, so the note points at them
// instead of repeating them.
func reviewNoteText(f verifydeliver.Fresh, ticket string) string {
	head := fmt.Sprintf("%s is built, every slice green, and no review round is open: open the gate and review", ticket)
	if f.Known && f.Fresh {
		return fmt.Sprintf("%s: run `jig gate %s`, then review it in this session with the review skill (`jig skill review`)", head, ticket)
	}
	why := "jig cannot tell whether this session built it"
	if f.Known {
		why = "this session " + whyWords(f.Why)
	}
	if f.Known && !f.AgentIDs {
		return fmt.Sprintf("%s: run `jig gate %s`, then have a separate session review it, not this session or a subagent of it (%s, and jig has no hook record of its tool calls); `jig gate` gives the command to run", head, ticket, why)
	}
	return fmt.Sprintf("%s: run `jig gate %s`, then have a subagent review it, not this session (%s); `jig gate` gives the Agent call and the prompt to pass", head, ticket, why)
}
