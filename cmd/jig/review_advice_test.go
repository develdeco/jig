package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestAdviceToSpawnSaysHowToWait: the advice that tells a session to spawn a
// reviewer says how to wait for it as Claude Code runs it, in the background
// whatever the session asks: in the same turn with TaskOutput, neither ending
// the turn nor scheduling a wakeup, and never stalling with another tool. It says so for a session that built the
// ticket and for one jig cannot tell, and never asks for a foreground run the
// harness does not give. A fresh session is told to review itself, with no
// subagent to wait for.
func TestAdviceToSpawnSaysHowToWait(t *testing.T) {
	t.Parallel()
	rule := []string{"TaskOutput (`block: true`", "load the tool with ToolSearch", "Do not end the turn or schedule a wakeup while it runs", "never call another tool to stall"}
	cases := []struct {
		name  string
		fresh verifydeliver.Fresh
		state string
		spawn bool
	}{
		{"built it", verifydeliver.Fresh{Known: true, Why: []string{"claimed slice a"}, AgentIDs: true}, freshNo, true},
		{"unknown", verifydeliver.Fresh{}, freshUnknown, true},
		{"fresh", verifydeliver.Fresh{Known: true, Fresh: true, AgentIDs: true}, freshYes, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := adviceFor(tc.fresh, "SHOP-1", "/work/shop")
			if a.State != tc.state {
				t.Errorf("state = %q, want %q", a.State, tc.state)
			}
			text := strings.Join(a.Lines, "\n")
			for _, want := range rule {
				if got := strings.Contains(text, want); got != tc.spawn {
					t.Errorf("advice has %q = %v, want %v:\n%s", want, got, tc.spawn, text)
				}
			}
			if strings.Contains(text, "run_in_background") {
				t.Errorf("advice asks for a foreground run, which Claude Code does not give:\n%s", text)
			}
		})
	}
}

// TestAdviceWithoutAgentIDsStartsASeparateSession: a session that built the
// ticket and that no hook reports (no agent ids) is told to start the reviewer
// as a session of its own, not a subagent, with the command that does it. The
// command, read by sh's rules, unsets each variable jig reads a session from
// and hands claude the reviewer's prompt as one word, a quote in the clone's
// path and all, with its input from /dev/null. A session jig has no record of
// keeps the subagent: no builder is that session.
func TestAdviceWithoutAgentIDsStartsASeparateSession(t *testing.T) {
	t.Parallel()
	const ticket, clone = "SHOP-1", "/work/o'brien/shop"
	a := adviceFor(verifydeliver.Fresh{Known: true, Why: []string{"claimed slice a"}}, ticket, clone)
	text := strings.Join(a.Lines, "\n")
	if a.State != freshNo || !strings.Contains(text, "claimed slice a") || !strings.Contains(text, "start a separate session to review "+ticket) ||
		strings.Contains(text, "spawn a subagent") || strings.Contains(text, "Agent tool") {
		t.Errorf("advice for a builder with no agent ids = %s, %q, want a separate session and why, no subagent", a.State, text)
	}
	var argv []string
	for _, l := range a.Lines {
		if strings.HasPrefix(l, "env ") {
			words, ok := strings.CutSuffix(l, " < /dev/null")
			if !ok {
				t.Errorf("the command %q does not take its input from /dev/null", l)
			}
			argv = shWords(t, words)
		}
	}
	want := []string{"env", "-u", "JIG_SESSION", "-u", "CLAUDE_CODE_SESSION_ID", "claude", "-p", reviewerPromptText(ticket, clone), "--model", "opus", "--permission-mode", "bypassPermissions"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the command's words = %q, want %q", argv, want)
	}
	if prompt := reviewerPromptText(ticket, clone); !strings.Contains(prompt, "In "+clone+",") || !strings.Contains(prompt, "jig review done "+ticket) {
		t.Errorf("prompt = %q, want the clone and the review verbs", prompt)
	}

	if unknown := adviceFor(verifydeliver.Fresh{}, ticket, clone); !strings.Contains(strings.Join(unknown.Lines, "\n"), "spawn a subagent") {
		t.Errorf("advice for a session jig has no record of = %q, want a subagent", unknown.Lines)
	}
}

// shWords splits line into words as a POSIX shell does for words with no
// expansion: blanks separate them, single quotes keep everything to the next
// one, and a backslash outside them keeps the next character.
func shWords(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quoted && c == '\'':
			quoted = false
		case quoted:
			word.WriteByte(c)
		case c == '\'':
			quoted, inWord = true, true
		case c == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case strings.ContainsRune("\"$`|&;<>()", rune(c)):
			t.Fatalf("shWords: %q has %q outside single quotes, which sh would read as more than a word", line, c)
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if quoted {
		t.Fatalf("shWords: %q leaves a single quote open", line)
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}
