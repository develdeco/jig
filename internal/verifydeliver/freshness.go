package verifydeliver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// Fresh says whether a harness session may review a ticket itself. jig starts
// no reviewer and judges independence only when a round is closed
// (JudgeIndependence), so this is the word before the review: a session that
// briefed, built or approved the ticket should have a subagent read the round,
// and one that touched none of it can read it in its own context.
//
// Known is false when jig cannot tell: the session is empty, or neither the
// ticket's journal nor the machine ledger has a line of it (a harness with no
// hooks). Fresh is true only for a known session with nothing of the ticket's
// work in its name. Why lists, in words, what makes a session not fresh.
type Fresh struct {
	Known bool
	Fresh bool
	Why   []string
}

// Freshness reads session's record against ticket.
//
// A session is fresh for ticket when its id carries no journal line of the
// ticket except the gate's, triage and publish lines, and no ledger line of
// the session that names the ticket changed a file or made a commit. A claim,
// a verification, an oracle run, an approval, a question, an answer, a note or
// a release is the session's own work on the ticket. A ledger tool line that
// read, searched or listed is no work: a reviewer reads the code with the same
// tools, and the review skill's own first call is `jig next`.
//
// The subagents of a session share its id (a hook payload tells them apart,
// the commands they run do not), so this is a fact about the whole session.
// That is why the advice for a session that is not fresh has a subagent do the
// review and the main session never read the round: if both read it, the
// ledger cannot say which of them closed it (LedgerReviewerAgent).
func Freshness(d Deps, ticket, session string) (Fresh, error) {
	if session == "" {
		return Fresh{}, nil
	}
	var why []string
	add := func(w string) {
		for _, have := range why {
			if have == w {
				return
			}
		}
		why = append(why, w)
	}

	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		return Fresh{}, err
	}
	for _, l := range lines {
		if l.Session != session || freshnessExempt(l.Event) {
			continue
		}
		add(journalWork(l))
	}

	seen := false
	if d.Home != "" {
		records, err := ledger.Read(d.Home)
		if err != nil {
			return Fresh{}, err
		}
		for _, l := range records {
			if l.Session != session {
				continue
			}
			seen = true
			if l.Ticket != ticket {
				continue
			}
			if w := ledgerWork(l); w != "" {
				add(w)
			}
		}
	}

	switch {
	case len(why) > 0:
		return Fresh{Known: true, Why: why}, nil
	case seen:
		return Fresh{Known: true, Fresh: true}, nil
	}
	return Fresh{}, nil
}

// freshnessExempt reports whether a journal event is no work on the ticket: the
// gate's own lines (a reviewer's session writes them), a triage decision, and
// the lines of publishing, with the two kinds of bookkeeping jig writes for a
// session, spend and the recordings of a run.
func freshnessExempt(event string) bool {
	switch event {
	case journal.EventGateOpen, journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean,
		journal.EventTriage,
		journal.EventPrBody, journal.EventPublishApproved, journal.EventPr, journal.EventPublishDone, journal.EventPublishPicks,
		journal.EventChangelog, journal.EventMemorize,
		journal.EventSpend, journal.EventRecorded:
		return true
	}
	return false
}

// journalWork says in words what a session did that a journal line records.
func journalWork(l journal.Line) string {
	switch l.Event {
	case journal.EventClaim:
		return "claimed slice " + orWord(l.Slice, "a slice")
	case journal.EventRelease:
		return "released slice " + orWord(l.Slice, "a slice")
	case journal.EventApproval:
		return "approved the brief"
	case journal.EventVerified:
		return "verified " + commitWord(l.Commit)
	case journal.EventVerifyPending:
		return "started the verification of " + commitWord(l.Commit)
	case journal.EventOracle:
		return "ran the oracle on " + commitWord(l.Commit)
	case journal.EventVerifyError:
		return "hit an error verifying " + commitWord(l.Commit)
	case journal.EventQuestion:
		return "asked question " + orWord(l.Ref, "of the person")
	case journal.EventAnswer:
		return "answered question " + orWord(l.Ref, "of a builder")
	case journal.EventNote:
		return "left a note"
	}
	return fmt.Sprintf("wrote a %s line", l.Event)
}

// ledgerWork says in words what a ledger line of the session records as work on
// the ticket: files changed, a commit made. "" for a line that is none.
func ledgerWork(l journal.Line) string {
	switch {
	case l.Event == journal.EventGitCommit:
		return "made commit " + commitWord(l.Commit)
	case l.Event == journal.EventClaim:
		return "claimed slice " + orWord(l.Slice, "a slice")
	case l.Event == journal.EventTool && len(l.Files) > 0:
		return "changed " + filepath.ToSlash(l.Files[0])
	case l.Event == journal.EventTool && commitsCode(l.Command):
		return "ran git commit"
	}
	return ""
}

// commitsCode reports whether the shell command line cmd runs `git commit`: the
// word git (any path, with or without .exe), then the first word that is no
// option, and no option's value, is commit. A commit a shell makes leaves a tool
// line that names no file.
func commitsCode(cmd string) bool {
	words := strings.Fields(cmd)
	for i, w := range words {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/")))
		if base != "git" && base != "git.exe" {
			continue
		}
		for j := i + 1; j < len(words); j++ {
			a := strings.Trim(words[j], `"'`)
			if a == "&&" || a == "||" || a == ";" || a == "|" {
				break
			}
			if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" {
				j++ // the option's value
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			if a == "commit" {
				return true
			}
			break
		}
	}
	return false
}

// commitWord is a commit as a person reads it: its first seven characters.
func commitWord(sha string) string {
	if sha == "" {
		return "a commit"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// orWord is s, or the fallback when s is empty.
func orWord(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
