package verify

import (
	"errors"
	"strings"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
)

// tailBytes is how much of an oracle's output the machine-local log keeps
// of a failed run.
const tailBytes = 4000

// summaryBytes is how much of a failure's tail the journal line carries.
const summaryBytes = 600

// outputTail keeps the last 4,000 bytes of an oracle's output: enough to
// show a failure, small enough for a log. (Copied from the frontier, which
// kept the same for a prompt and a summary.)
func outputTail(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > tailBytes {
		out = out[len(out)-tailBytes:]
	}
	return out
}

// oneLine flattens the end of s to one line of at most about 600 bytes, for
// a journal summary. The kept part starts at a line boundary, so two
// identical failures summarize identically and the stall counter can match
// them. (Copied from the frontier.)
func oneLine(s string) string { return oneLineMax(s, summaryBytes) }

// oneLineMax is oneLine with a bound of max bytes.
func oneLineMax(s string, max int) string {
	if len(s) > max {
		s = s[len(s)-max:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// failureOutput is a failed run's tail and summary, from the bounded tail of
// its output: the end of the output, then, for a run the wedge guard killed,
// the reason, which is not in the output and keeps its own room in the
// summary, shortened to its ending (the full error embeds the oracle
// command).
func failureOutput(out string, err error) (tail, summary string) {
	old := failureTail(out, err)
	reason := killedReason(err)
	if reason == "" {
		return old, oneLine(old)
	}
	return old, joinReason(oneLineMax(outputTail(out), summaryBytes-len(reason)-1), reason)
}

// killedReason is the wedge guard's own words, "did not finish within
// <limit>", when err is its kill of the run, "" otherwise. A kill is read
// from the error (envrun.ErrTimedOut), never from its text: the text embeds
// the oracle command, which can hold any words.
func killedReason(err error) string {
	var t *envrun.TimedOutError
	if errors.As(err, &t) {
		return t.Reason()
	}
	return ""
}

// failureCause is the journal cause of a run that ended with err: timeout for
// the wedge guard's kill, "" for a run that ended with the test's own result.
// A stopped run is journaled where it is seen. Read from the typed error, never
// from text.
func failureCause(err error) string {
	if killedReason(err) != "" {
		return journal.CauseTimeout
	}
	return ""
}

// joinReason is body then reason on one line.
func joinReason(body, reason string) string {
	return strings.Join(strings.Fields(body+" "+reason), " ")
}

// failureSignature is a failed run's stall signature. It reads the start of
// the summary, so a killed run's reason, which ends the summary, is added
// apart from it (without its variable parts, as Signature drops digits):
// two kills of one slice match however many new lines precede the reason,
// and a kill does not match a plain failure.
func failureSignature(slice, summary string, err error) string {
	reason := killedReason(err)
	if reason == "" {
		return outcome.Signature(slice, outcome.CodeBug, summary)
	}
	body := strings.TrimSpace(strings.TrimSuffix(summary, reason))
	gist := outcome.Signature("", "", reason)
	gist = gist[strings.LastIndex(gist, "|")+1:]
	return outcome.Signature(slice, outcome.CodeBug, body) + "|" + gist
}
