package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// namedReadsRig is a fixture clone on branch, with the ticket store open.
func namedReadsRig(t *testing.T, branch string) (*fixture.Fixture, env, *store.Store) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	if _, err := gitx.Run(fx.RepoDir, "checkout", "-q", "-b", branch); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return fx, testEnv(fx.Home).inDir(fx.RepoDir), st
}

// toolCall sends one tool-call hook, as Claude Code does after a command, and
// returns the ticket jig recorded the call on and what its log says went
// wrong.
func toolCall(t *testing.T, fx *fixture.Fixture, e env, command string) (ticket, logged string) {
	t.Helper()
	out, code := runMain(t, e, hookPayload(t, fx, "PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}}), "observe", "--harness", "claude")
	if code != 0 {
		t.Fatalf("tool hook: exit %d\n%s", code, out)
	}
	lines := ledgerEvents(t, fx.Home, journal.EventTool)
	if len(lines) == 0 {
		t.Fatal("the tool hook recorded no tool line")
	}
	data, err := os.ReadFile(filepath.Join(home.LogsDir(fx.Home), "observe.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return lines[len(lines)-1].Ticket, string(data)
}

// writeStoreFile writes the store file at the slash path rel.
func writeStoreFile(t *testing.T, st *store.Store, rel, content string) {
	t.Helper()
	path := filepath.Join(st.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestToolCallTellsAStackedTicketItsParentMoved: a ticket with a stacked
// blocker whose branch moved past the ticket's hears to restack at its next
// tool call.
func TestToolCallTellsAStackedTicketItsParentMoved(t *testing.T) {
	t.Parallel()
	fx, e, st := namedReadsRig(t, "parent-work")
	commit := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fx.RepoDir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, a := range [][]string{{"add", "--", name}, {"commit", "-q", "-m", name}} {
			if _, err := gitx.Run(fx.RepoDir, a...); err != nil {
				t.Fatal(err)
			}
		}
	}
	commit("parent.txt")
	if _, err := gitx.Run(fx.RepoDir, "checkout", "-q", "-b", "JIG-1-child", "main"); err != nil {
		t.Fatal(err)
	}
	commit("child.txt")
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Parent", Branch: "parent-work"}); err != nil {
		t.Fatal(err)
	}
	writeStoreFile(t, st, "tickets/JIG-1/ticket.yaml", "schema_version: 1\ntitle: Child\nblocked_by:\n  - ticket: JIG-2\n    kind: stacked\n")

	out, code := runMain(t, e, hookPayload(t, fx, "PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git status"}}), "observe", "--harness", "claude")
	if code != 0 || !strings.Contains(out, "JIG-1 is stacked on JIG-2, whose branch moved past it: restack onto `parent-work`") {
		t.Errorf("exit %d, the tool hook said %q; want the restack onto parent-work", code, out)
	}
}

// TestToolCallReadsOnlyNamedTickets: a tool call reads only the tickets it
// names. Each case plants, in a ticket or a tracker record the call does not
// name, something that changes what the hook does if it is read.
func TestToolCallReadsOnlyNamedTickets(t *testing.T) {
	t.Parallel()

	t.Run("a branch led by a ticket's id", func(t *testing.T) {
		t.Parallel()
		fx, e, st := namedReadsRig(t, "JIG-1-work")
		// Another ticket lists the id as an old one of its own: a walk of the
		// tickets finds two claimants and binds the branch to neither.
		if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Unnamed", Aliases: []string{fx.Ticket}}); err != nil {
			t.Fatal(err)
		}
		ticket, logged := toolCall(t, fx, e, "git status")
		if ticket != fx.Ticket || logged != "" {
			t.Errorf("recorded on %q, observe.log %q; want the call on %s and nothing wrong", ticket, logged, fx.Ticket)
		}
	})

	t.Run("a branch led by an issue number", func(t *testing.T) {
		t.Parallel()
		fx, e, st := namedReadsRig(t, "48-work")
		cfgPath := filepath.Join(fx.StoreDir, "project.yaml")
		cfg, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		declared := strings.Replace(string(cfg), "trackers: []", "trackers:\n    - github:\n        repo: acme/tracking\n        project: https://github.com/users/acme/projects/1", 1)
		if declared == string(cfg) {
			t.Fatalf("the fixture's project.yaml declares no empty trackers list:\n%s", cfg)
		}
		writeStoreFile(t, st, "project.yaml", declared)
		if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Unnamed"}); err != nil {
			t.Fatal(err)
		}
		writeStoreFile(t, st, "trackers/github/acme/tracking/48/record.yaml", "ticket: "+fx.Ticket+"\nrepo: acme/tracking\nissue: 48\n")
		// Another repository's issue 48 is another ticket's: reading every
		// record finds two tickets for the number and binds the branch to
		// neither.
		writeStoreFile(t, st, "trackers/github/acme/elsewhere/48/record.yaml", "ticket: JIG-2\nrepo: acme/elsewhere\nissue: 48\n")
		ticket, logged := toolCall(t, fx, e, "git status")
		if ticket != fx.Ticket || logged != "" {
			t.Errorf("recorded on %q, observe.log %q; want the call on %s and nothing wrong", ticket, logged, fx.Ticket)
		}
	})

	t.Run("a chart task with no stacked blocker", func(t *testing.T) {
		t.Parallel()
		fx, e, st := namedReadsRig(t, "JIG-1-work")
		if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Merged blocker"}); err != nil {
			t.Fatal(err)
		}
		writeStoreFile(t, st, "tickets/JIG-1/ticket.yaml", "schema_version: 1\ntitle: The task\nblocked_by:\n  - ticket: JIG-2\n    kind: merged\n")
		writeStoreFile(t, st, "charts/plan/tickets.yaml", "issue: JIG-3\ntickets:\n  - id: JIG-1\n    title: The task\n")
		// The chart's issue has a record that cannot be decoded: a plan of the
		// task's stack reads it and the hook logs why it failed.
		writeStoreFile(t, st, "tickets/JIG-3/ticket.yaml", "no_such_key: true\n")
		ticket, logged := toolCall(t, fx, e, "git status")
		if ticket != fx.Ticket || logged != "" {
			t.Errorf("recorded on %q, observe.log %q; want the call on %s and nothing wrong", ticket, logged, fx.Ticket)
		}
	})
}
