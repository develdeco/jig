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
		{"key", "area key the store's one project.yaml keys: entry declares (standalone only); default: derived from the project name", false},
		{"clone", "name=path clone mapping; repeatable", false},
	}, false},
	{"ticket", "mint a new ticket: jig ticket new --title <t>", []flagSpec{
		{"title", "ticket title (required)", false},
		{"body", "ticket body/description", false},
		{"key", "key to mint under (required when project.yaml declares more than one)", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"graduate", "create tickets from a chart: jig graduate <chart>", []flagSpec{
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"solve", "run the full chain: run, gate, publish", []flagSpec{
		{"yes", "skip the interactive publish confirm and finding triage", false},
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
	{"requeue", "requeue slices touched by a brief edit, or one stalled/env-blocked slice", []flagSpec{
		{"from-brief-diff", "requeue slices whose brief section hash changed", false},
		{"slice", "requeue one stalled or env-blocked slice by id", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"gate", "run a gate round over the ticket's branch", []flagSpec{
		{"early", "gate before the frontier is fully green", false},
		{"branch", "review this branch, built outside jig, and adopt it as the ticket's own (recorded on the first round)", false},
		{"intent", "explicit intent text, recorded as intent.md (refused when the ticket has a brief.md); with no brief, --intent or --doc, a reviewer round reads your local Claude Code sessions for this repo and has a model summarize the best match into intent.md", false},
		{"doc", "doc file whose content becomes the ticket's explicit intent, recorded as intent.md (refused when the ticket has a brief.md)", false},
		{"pr", "pr number (not implemented in v0.1)", true},
		{"yes", "keep every finding jig can route on its own, without the triage prompt", false},
		{"backend", "session backend for the reviewer: fake, headless, or herdr", false},
		{"scenario", "scenario dir for the fake gate source", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"publish", "reconcile, revalidate, and open or update the PR", []flagSpec{
		{"yes", "skip the interactive confirm", false},
		{"backend", "session backend that picks the recordings the pull request shows: fake, headless, or herdr", false},
		{"scenario", "scenario dir for the fake backend", false},
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
	{"trackers", "jig trackers sync: sync the store onto GitHub issues", []flagSpec{
		{"dry-run", "read everything and report what would change, writing nothing", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
	}, false},
	{"store", "jig store migrate: migrate a v1 store onto schema_version 2", []flagSpec{
		{"map", "the rename map file (old id to new key)", false},
		{"dry-run", "print the rename map and every file the migration would move, delete or rewrite, changing nothing", false},
		{"store", "explicit store path", false},
		{"project", "project name, resolved via the machine mapping", false},
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
	case "trackers":
		return "trackers sync"
	case "store":
		return "store migrate"
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
