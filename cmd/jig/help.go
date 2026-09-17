package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
)

// flagSpec is one flag a command accepts: its name (without leading dashes),
// a one-line usage description, and whether it is hidden from help output.
type flagSpec struct {
	Name   string
	Usage  string
	Hidden bool
}

// cmdSpec is one command's help-table entry.
type cmdSpec struct {
	Name    string
	Summary string
	Flags   []flagSpec
	Hidden  bool
}

// commandTable lists every jig subcommand, in help order. Each cmdX registers
// its own flags; TestCommandTableFlagsMatchRegistration keeps this table in
// step with them. Hidden entries work but are left out of help.
var commandTable = []cmdSpec{
	{"init", "initialize a store (standalone, or store + clones)", []flagSpec{
		{"standalone", "create a sibling tickets store next to the current repo", false},
		{"store", "store path to initialize (used with --clone)", false},
		{"clone", "name=path clone mapping; repeatable", false},
	}, false},
	{"ticket", "mint a new ticket: jig ticket new --title <t>", []flagSpec{
		{"title", "ticket title (required)", false},
		{"body", "ticket body", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"solve", "run the full chain: run, gate, publish", []flagSpec{
		{"yes", "skip the interactive publish confirm", false},
		{"answer", "answer a pending question: --answer <qid> <text>", false},
		{"backend", "session backend: fake, headless, or herdr", false},
		{"scenario", "scenario dir for the fake backend", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"run", "dispatch the frontier of queued slices", []flagSpec{
		{"answer", "answer a pending question: --answer <qid> <text>", false},
		{"backend", "session backend: fake, headless, or herdr", false},
		{"scenario", "scenario dir for the fake backend", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"requeue", "requeue slices touched by a brief edit", []flagSpec{
		{"from-brief-diff", "requeue slices whose brief section hash changed", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"gate", "run a gate round over the ticket's branch", []flagSpec{
		{"early", "gate before the frontier is fully green", false},
		{"branch", "validate this branch instead of jig/<ticket>", false},
		{"doc", "brief doc path, used together with --branch", false},
		{"pr", "pr number (not implemented in v0.1)", true},
		{"scenario", "scenario dir for the fake gate source", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"publish", "reconcile, revalidate, and open the PR", []flagSpec{
		{"yes", "skip the interactive confirm", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"status", "print a ticket's slice and question state", []flagSpec{
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"validate", "check a ticket's brief, slices, and manifest", []flagSpec{
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"version", "print jig's version, commit, and go runtime", nil, false},
	{"skills", "jig skills install: ship the session skills with the binary", []flagSpec{
		{"project", "install under ./.claude/skills of the current directory", false},
		{"dest", "install under <dir>/<name>/SKILL.md instead of the default location", false},
	}, false},
	{"_screen", "hidden PreToolUse hook: reads a tool call on stdin", nil, true},
}

// usageBlocks renders the AXI-register usage block printed by bare "jig"
// or "jig -h": the command table, one flags{<cmd>} table per command, and
// three worked examples, skipping hidden commands and flags.
func usageBlocks() []string {
	blocks := []string{"usage: jig <command> [flags]"}

	var rows [][]string
	for _, c := range commandTable {
		if c.Hidden {
			continue
		}
		rows = append(rows, []string{c.Name, c.Summary})
	}
	blocks = append(blocks, axi.Table("commands", []string{"name", "summary"}, rows))

	for _, c := range commandTable {
		if c.Hidden {
			continue
		}
		var frows [][]string
		for _, f := range c.Flags {
			if f.Hidden {
				continue
			}
			frows = append(frows, []string{"--" + f.Name, f.Usage})
		}
		blocks = append(blocks, axi.Table(fmt.Sprintf("flags{%s}", c.Name), []string{"flag", "usage"}, frows))
	}

	blocks = append(blocks, axi.Help(
		`jig ticket new --title "Fix the thing"`,
		"jig run T-1",
		"jig solve T-1 --yes",
	))
	return blocks
}

// flagSetName returns the name a command passes to newFlagSet: its table name,
// or "<cmd> <sub>" for the commands whose flags belong to their one
// subcommand.
func flagSetName(cmdName string) string {
	switch cmdName {
	case "ticket":
		return "ticket new"
	case "skills":
		return "skills install"
	default:
		return cmdName
	}
}

// flagsBlockFor renders the usage line and visible flags for the command
// whose FlagSet is named fsName, for `jig <command> -h`.
func flagsBlockFor(fsName string) []string {
	usage := fmt.Sprintf("usage: jig %s [flags]", fsName)
	for _, c := range commandTable {
		if flagSetName(c.Name) != fsName {
			continue
		}
		var frows [][]string
		for _, f := range c.Flags {
			if f.Hidden {
				continue
			}
			frows = append(frows, []string{"--" + f.Name, f.Usage})
		}
		return []string{usage, axi.Table(fmt.Sprintf("flags{%s}", c.Name), []string{"flag", "usage"}, frows)}
	}
	return []string{usage}
}
