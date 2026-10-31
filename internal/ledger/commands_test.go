package ledger

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// TestRunsJig: the command lines a harness records that count as running a
// jig verb for a ticket.
func TestRunsJig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd    string
		ticket string
		verb   []string
		want   bool
	}{
		{"jig review context JIG-1", "JIG-1", []string{"review", "context"}, true},
		{"jig review context", "JIG-1", []string{"review", "context"}, true},
		{"jig review context --session s-1 JIG-1", "JIG-1", []string{"review", "context"}, true},
		{"jig.exe review context JIG-1 --json", "JIG-1", []string{"review", "context"}, true},
		{"/usr/local/bin/jig review context JIG-1", "JIG-1", []string{"review", "context"}, true},
		{`"C:\tools\jig.exe" review context JIG-1`, "JIG-1", []string{"review", "context"}, true},
		{"cd repo && jig review context JIG-1", "JIG-1", []string{"review", "context"}, true},
		{"jig review context JIG-1 && echo done", "JIG-1", []string{"review", "context"}, true},
		{"jig review context JIG-2", "JIG-1", []string{"review", "context"}, false},
		{"jig review context JIG-2 ; jig status JIG-1", "JIG-1", []string{"review", "context"}, false},
		{"jig review done JIG-1", "JIG-1", []string{"review", "context"}, false},
		{"jig finding JIG-1", "JIG-1", []string{"review", "context"}, false},
		{"git review context JIG-1", "JIG-1", []string{"review", "context"}, false},
		{"jig review", "JIG-1", []string{"review", "context"}, false},
		{"", "JIG-1", []string{"review", "context"}, false},

		{"jig claim JIG-1 --slice a --unattended", "JIG-1", []string{"claim"}, true},
		{"jig claim --slice a --unattended JIG-1", "JIG-1", []string{"claim"}, true},
		{"jig claim JIG-2 --slice a", "JIG-1", []string{"claim"}, false},
		{"jig claimed JIG-1", "JIG-1", []string{"claim"}, false},
		{"jig claim --slice a", "", []string{"claim"}, true},
		{"jig claim JIG-2 --slice a", "", []string{"claim"}, true},
		{"jig ask JIG-1 --slice a", "JIG-1", []string{"ask"}, true},
		{"jig answer JIG-1 q1 yes", "JIG-1", []string{"ask"}, false},
	}
	for _, c := range cases {
		if got := RunsJig(c.cmd, c.ticket, c.verb...); got != c.want {
			t.Errorf("RunsJig(%q, %q, %v) = %v, want %v", c.cmd, c.ticket, c.verb, got, c.want)
		}
	}
}

// TestAgentOf: the agent a command of a session is read back from, when the
// ledger's tool lines agree on one.
func TestAgentOf(t *testing.T) {
	t.Parallel()
	at := func(s string) string { return "2026-01-02T03:04:" + s + "Z" }
	since, _ := time.Parse(time.RFC3339Nano, at("10"))
	claims := func(l journal.Line) bool { return RunsJig(l.Command, "T-1", "claim") }
	tool := func(ts, session, agent, command string) journal.Line {
		return journal.Line{TS: ts, Event: journal.EventTool, Session: session, Agent: agent, Command: command}
	}
	cases := []struct {
		name      string
		lines     []journal.Line
		session   string
		wantAgent string
		wantOK    bool
	}{
		{"one agent", []journal.Line{tool(at("11"), "s1", "a-1", "jig claim T-1 --slice a")}, "s1", "a-1", true},
		{"the session's own agent is the empty one", []journal.Line{tool(at("11"), "s1", "", "jig claim T-1")}, "s1", "", true},
		{"the same agent twice", []journal.Line{tool(at("11"), "s1", "a-1", "jig claim T-1"), tool(at("12"), "s1", "a-1", "jig claim T-1 --take")}, "s1", "a-1", true},
		{"two agents say nothing", []journal.Line{tool(at("11"), "s1", "a-1", "jig claim T-1"), tool(at("12"), "s1", "", "jig claim T-1")}, "s1", "", false},
		{"a line before since is not read", []journal.Line{tool(at("09"), "s1", "a-2", "jig claim T-1"), tool(at("11"), "s1", "a-1", "jig claim T-1")}, "s1", "a-1", true},
		{"only lines before since", []journal.Line{tool(at("09"), "s1", "a-2", "jig claim T-1")}, "s1", "", false},
		{"another session's line is not read", []journal.Line{tool(at("11"), "s2", "a-2", "jig claim T-1"), tool(at("12"), "s1", "a-1", "jig claim T-1")}, "s1", "a-1", true},
		{"another command is not read", []journal.Line{tool(at("11"), "s1", "a-2", "go test ./..."), tool(at("12"), "s1", "a-1", "jig claim T-1")}, "s1", "a-1", true},
		{"a line that is not a tool call is not read", []journal.Line{{TS: at("11"), Event: journal.EventHook, Session: "s1", Agent: "a-2", Command: "jig claim T-1"}}, "s1", "", false},
		{"no session", []journal.Line{tool(at("11"), "", "a-1", "jig claim T-1")}, "", "", false},
		{"nothing", nil, "s1", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, l := range c.lines {
				if err := Append(root, l); err != nil {
					t.Fatal(err)
				}
			}
			agent, ok, err := AgentOf(root, 0, c.session, since, claims)
			if err != nil || agent != c.wantAgent || ok != c.wantOK {
				t.Errorf("AgentOf = %q, %v, %v; want %q, %v", agent, ok, err, c.wantAgent, c.wantOK)
			}
		})
	}
}
