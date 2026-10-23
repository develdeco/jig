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

// usageBlocks renders the AXI-register usage block printed by bare "jig"
// or "jig -h": the registered commands, one flags{<cmd>} table per command,
// and three worked examples, skipping hidden commands and flags. Commands
// appear in registry order (verb.Order, then name).
func usageBlocks() []string {
	blocks := []string{"usage: jig <command> [flags]"}
	table := commandTable()

	var rows [][]string
	for _, c := range table {
		if c.Hidden {
			continue
		}
		rows = append(rows, []string{c.Name, c.Summary})
	}
	blocks = append(blocks, axi.Table("commands", []string{"name", "summary"}, rows))

	for _, c := range table {
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

// flagSetName returns the name a command passes to newFlagSet: the FlagSet
// its registration names ("ticket new" for the commands whose flags belong to
// their one subcommand), else the command's own name.
func flagSetName(cmdName string) string {
	if v, ok := verbNamed(cmdName); ok && v.FlagSet != "" {
		return v.FlagSet
	}
	return cmdName
}

// flagsBlockFor renders the usage line and visible flags for the command
// whose FlagSet is named fsName, for `jig <command> -h`.
func flagsBlockFor(fsName string) []string {
	usage := fmt.Sprintf("usage: jig %s [flags]", fsName)
	for _, c := range commandTable() {
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
