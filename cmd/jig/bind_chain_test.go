package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestChartRunCallsBindToTheirTicket: a main session and two subagents, each
// subagent in a worktree of its own on its ticket's branch but calling through
// the session's directory, each claiming a slice of its ticket. Every
// subagent call, its first included, is on its ticket in what jig events
// says of that ticket, once each though every hook fired twice; the session's
// calls after the claims are released are on none; each subagent's stop
// writes its spend line on its ticket; and jig tui, with stdout not a
// terminal, prints for each ticket an activity of its own calls only.
func TestChartRunCallsBindToTheirTicket(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	trees := map[string]string{}
	for _, id := range []string{"JIG-1", "JIG-2"} {
		trees[id] = filepath.Join(fx.Dir, "wt-"+id)
		if _, err := gitx.Run(fx.RepoDir, "worktree", "add", "-q", "-b", "jig/"+id, trees[id]); err != nil {
			t.Fatal(err)
		}
	}
	e := testEnv(fx.Home).inDir(fx.RepoDir).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))
	transcript := filepath.Join(fx.Dir, "t.jsonl")
	for agent, tokens := range map[string]int{"a-1": 4000, "a-2": 9000} {
		path := filepath.Join(fx.Dir, "t", "subagents", "agent-"+agent+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"assistant","sessionId":"s1","requestId":"req_%[1]s","message":{"id":"m-%[1]s","model":"claude-sonnet-4-5","role":"assistant","usage":{"input_tokens":%[2]d,"output_tokens":10}}}`, agent, tokens)
		if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// fire is one hook event at the n-th second, from the session's
	// directory, run twice as a machine and a project hook both would, the second stamped a few milliseconds after the first.
	fire := func(n int, event, agent string, extra map[string]any) {
		t.Helper()
		p := map[string]any{"session_id": "s1", "cwd": fx.RepoDir, "hook_event_name": event, "transcript_path": transcript}
		if agent != "" {
			p["agent_id"] = agent
		}
		for k, v := range extra {
			p[k] = v
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if out, code := runMain(t, e.withClock(fixedClock(mustTime(t, eventsAt(n)).Add(time.Duration(i)*30*time.Millisecond))), string(data), "observe", "--harness", "claude"); code != 0 {
				t.Fatalf("observe %s at %d: exit %d\n%s", event, n, code, out)
			}
		}
	}
	call := func(n int, agent, command, why string) {
		t.Helper()
		fire(n, "PostToolUse", agent, map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command, "description": why}})
	}
	claim := func(n int, ticket string) {
		t.Helper()
		if err := journal.Append(st, ticket, journal.Line{TS: eventsAt(n), Event: journal.EventClaim, Slice: "a", Session: "s1"}); err != nil {
			t.Fatal(err)
		}
	}
	release := func(n int, ticket string) {
		t.Helper()
		if err := journal.Append(st, ticket, journal.Line{TS: eventsAt(n), Event: journal.EventRelease, Slice: "a", Session: "s1"}); err != nil {
			t.Fatal(err)
		}
	}

	fire(1, "SessionStart", "", map[string]any{"source": "startup", "model": "m"})
	call(2, "a-1", "jig here", "First look at one")
	call(3, "a-2", "jig here", "First look at two")
	claim(4, "JIG-1")
	call(4, "a-1", "jig claim JIG-1 --slice a --unattended", "Claim one")
	claim(5, "JIG-2")
	call(5, "a-2", "jig claim JIG-2 --slice a --unattended", "Claim two")
	call(6, "a-1", "go test ./...", "Test one")
	call(7, "a-2", "go build ./...", "Build two")
	fire(8, "SubagentStop", "a-1", nil)
	fire(9, "SubagentStop", "a-2", nil)
	release(10, "JIG-1")
	release(10, "JIG-2")
	call(11, "", "git log", "After the claims")
	call(12, "", "git status", "After the claims, again")

	own := map[string][]string{
		"JIG-1": {"jig here", "jig claim JIG-1 --slice a --unattended", "go test ./..."},
		"JIG-2": {"jig here", "jig claim JIG-2 --slice a --unattended", "go build ./..."},
	}
	for ticket, commands := range own {
		// jig events: the ticket's own calls, once each, in order, each with
		// its agent, and nothing of the other ticket's or the session's.
		out, code := runMain(t, e, "", "events", ticket)
		if code != 0 {
			t.Fatalf("jig events %s: exit %d\n%s", ticket, code, out)
		}
		var got []string
		for _, env := range decodeStream(t, out) {
			if env.Src == "ledger" && env.Event == journal.EventTool {
				got = append(got, env.Agent+": "+env.Command)
				if env.Ticket != ticket {
					t.Errorf("jig events %s shows a call on %q: %s", ticket, env.Ticket, env.Command)
				}
			}
		}
		agent := map[string]string{"JIG-1": "a-1", "JIG-2": "a-2"}[ticket]
		var want []string
		for _, c := range commands {
			want = append(want, agent+": "+c)
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("jig events %s shows the calls\n%s\nwant\n%s", ticket, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}

		// The subagent's stop wrote its spend line on its ticket.
		spends := 0
		lines, err := journal.Read(st, ticket)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if l.Event == journal.EventSpend && l.Agent == agent {
				spends++
			}
		}
		if spends != 1 {
			t.Errorf("%s has %d spend lines of %s, want the one its stop wrote", ticket, spends, agent)
		}
	}

	// The session's own calls after the releases, and the calls before any
	// claim of an agent that bound nothing, are on no ticket in the ledger.
	for _, l := range ledgerEvents(t, fx.Home, journal.EventTool) {
		if l.Agent == "" && l.Ticket != "" {
			t.Errorf("the session's call %q is on %q, want none", l.Command, l.Ticket)
		}
	}

	// jig tui, on a pipe: each ticket's screen holds its own agent's calls.
	for ticket, commands := range own {
		_ = commands
		keys, screens, wait := startTUI(t, e, ticket)
		why := map[string][]string{
			"JIG-1": {"First look at one", "Claim one", "Test one"},
			"JIG-2": {"First look at two", "Claim two", "Build two"},
		}
		screens.until(t, why[ticket][2])
		frame := screens.String()
		other := "JIG-1"
		if ticket == "JIG-1" {
			other = "JIG-2"
		}
		for _, w := range why[ticket] {
			if n := strings.Count(lastFrame(frame), w); n != 1 {
				t.Errorf("the %s screen shows %q %d times, want once:\n%s", ticket, w, n, lastFrame(frame))
			}
		}
		for _, w := range append(why[other], "After the claims") {
			if strings.Contains(lastFrame(frame), w) {
				t.Errorf("the %s screen shows %q, a call that is not its own:\n%s", ticket, w, lastFrame(frame))
			}
		}
		keys.Write([]byte("q"))
		if code := wait(); code != 0 {
			t.Fatalf("jig tui %s: exit %d\n%s", ticket, code, screens.String())
		}
	}
}

// lastFrame is the last screen a jig tui on a pipe printed.
func lastFrame(out string) string {
	frames := strings.Split(out, "\n\f\n")
	for i := len(frames) - 1; i >= 0; i-- {
		if strings.TrimSpace(frames[i]) != "" {
			return frames[i]
		}
	}
	return out
}
