package main

import (
	"fmt"

	"github.com/develdeco/jig/axi"
)

// flagSpec is one flag a command accepts: its name (without leading dashes)
// and a one-line usage description. commandTable is the single source that
// drives both flag registration (each command's cmdX function) and the
// printed help text, so they cannot drift apart.
type flagSpec struct {
	Name  string
	Usage string
}

// cmdSpec is one command's help-table entry.
type cmdSpec struct {
	Name    string
	Summary string
	Flags   []flagSpec
}

// commandTable lists every jig subcommand, in help order.
var commandTable = []cmdSpec{
	{"init", "initialize a store (standalone, or store + clones)", []flagSpec{
		{"standalone", "create a sibling tickets store next to the current repo"},
		{"store", "store path to initialize (used with --clone)"},
		{"clone", "name=path clone mapping; repeatable"},
	}},
	{"ticket", "mint a new ticket: jig ticket new --title <t>", []flagSpec{
		{"title", "ticket title (required)"},
		{"body", "ticket body"},
		{"store", "explicit store path"},
	}},
	{"solve", "run the full chain: run, gate, publish", []flagSpec{
		{"yes", "skip the interactive publish confirm"},
		{"answer", "answer a pending question: --answer <qid> <text>"},
		{"backend", "session backend: fake, headless, or herdr"},
		{"scenario", "scenario dir for the fake backend"},
		{"store", "explicit store path"},
	}},
	{"run", "dispatch the frontier of queued slices", []flagSpec{
		{"answer", "answer a pending question: --answer <qid> <text>"},
		{"backend", "session backend: fake, headless, or herdr"},
		{"scenario", "scenario dir for the fake backend"},
		{"store", "explicit store path"},
	}},
	{"requeue", "requeue slices touched by a brief edit", []flagSpec{
		{"from-brief-diff", "requeue slices whose brief section hash changed"},
		{"store", "explicit store path"},
	}},
	{"gate", "run a gate round over the ticket's branch", []flagSpec{
		{"early", "gate before the frontier is fully green"},
		{"branch", "validate this branch instead of jig/<ticket>"},
		{"doc", "brief doc path, used together with --branch"},
		{"pr", "pr number (not implemented in v0.1)"},
		{"scenario", "scenario dir for the fake gate source"},
		{"store", "explicit store path"},
	}},
	{"publish", "reconcile, revalidate, and open the PR", []flagSpec{
		{"yes", "skip the interactive confirm"},
		{"store", "explicit store path"},
	}},
	{"status", "print a ticket's slice and question state", []flagSpec{
		{"store", "explicit store path"},
	}},
	{"validate", "check a ticket's brief, slices, and manifest", []flagSpec{
		{"store", "explicit store path"},
	}},
	{"_screen", "hidden PreToolUse hook: reads a tool call on stdin", nil},
}

// usageBlocks renders the AXI-register usage block printed by bare "jig"
// or "jig -h": the command table, one flags{<cmd>} table per command, and
// three worked examples.
func usageBlocks() []string {
	blocks := []string{"usage: jig <command> [flags]"}

	var rows [][]string
	for _, c := range commandTable {
		rows = append(rows, []string{c.Name, c.Summary})
	}
	blocks = append(blocks, axi.Table("commands", []string{"name", "summary"}, rows))

	for _, c := range commandTable {
		var frows [][]string
		for _, f := range c.Flags {
			frows = append(frows, []string{"--" + f.Name, f.Usage})
		}
		blocks = append(blocks, axi.Table(fmt.Sprintf("flags{%s}", c.Name), []string{"flag", "usage"}, frows))
	}

	blocks = append(blocks, axi.Help(
		"jig run JIG-1 --backend fake --scenario ./scenario",
		"jig gate JIG-1 --early",
		"jig publish JIG-1 --yes",
	))
	return blocks
}
