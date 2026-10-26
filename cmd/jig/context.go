package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/slicectx"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"context", "print what a slice needs to start: its brief sections, the earlier slices, the oracle, failed attempts and questions", []flagSpec{
			{"slice", sliceFlagUsage, false},
			{"json", "print one JSON object instead of blocks", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 311,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdContext(e, args, stdout)
		},
	})
}

// cmdContext implements `jig context [<ticket>] [--slice s] [--json]`: what
// slice.json carried when jig dispatched a builder, pulled by the builder
// instead. It reads the store and the clone and runs no context tool: the
// code is the session's to find with its own tools, and the help says so
// (ADR 0034). In a project of several repos it also names the repo the slice
// builds in and that repo's clone, wherever the command is run from.
func cmdContext(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "context")
	jsonOut := fs.Bool("json", false, "print one JSON object instead of blocks")
	flags := addWorkFlags(fs, true, false)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, err := atMostOne(positionals, "jig context", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	sl, err := w.slice()
	if err != nil {
		return renderErr(stdout, err)
	}

	repo, dir := sliceHome(w, sl)
	c, err := slicectx.Build(slicectx.Input{
		Store:   w.St,
		Ticket:  w.Ticket,
		Slice:   sl.ID,
		Repo:    repo,
		RepoDir: dir,
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	if len(w.Cfg.Repos) > 1 {
		// Which repo of the project the slice builds in, and where, is what a
		// session in the orchestrator or in the other repo needs to be told.
		c.Repo, c.Clone = repo, dir
	}
	if *jsonOut {
		data, err := json.Marshal(c)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	blocks := c.Blocks()
	help := contextHelp(c)
	if line := otherRepoHelp(w, sl, repo, dir); line != "" {
		help = append([]string{line}, help...)
	}
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}

// contextHelp is the next step for the slice a context describes, and where
// the code it touches comes from: the session's own tools, starting from what
// the earlier slices changed when there are some.
func contextHelp(c slicectx.Context) []string {
	var help []string
	if c.Slice.State == "queued" {
		help = append(help, fmt.Sprintf("Claim it first: `jig claim %s --slice %s`", c.Ticket, c.Slice.ID))
	}
	find := "Find the code the goal touches with your own tools (search, a code graph, your editor's symbols)"
	if len(c.EarlierSlices) > 0 {
		find += ", starting from the files the earlier slices changed"
	}
	help = append(help, find)
	return append(help, fmt.Sprintf(
		"Test at the seams and commit with the trailer `Jig-Slice: %s`; jig runs the oracle on each commit. `jig ask` takes a question to the person",
		c.Slice.ID))
}
