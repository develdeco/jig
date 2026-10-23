package main

import (
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec:  cmdSpec{"attach", "jig attach: set up the jig project you are in for your agents, as setup --here does; refuses outside a project (a folder under a jig.yaml)", setupFlagSpecs(false), false},
		Order: 501,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdAttach(e, args, stdout, stdin)
		},
	})
}

// cmdAttach implements `jig attach`: setup --here, for a folder inside a jig
// project, which is a folder with a jig.yaml marker at or above it. With no
// piece named it sets up all four, in the untracked form.
func cmdAttach(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "attach")
	var p setupParams
	bindSetupFlags(fs, &p, false)
	names, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	p.names = names
	p.here = true

	cwd, err := e.getwd()
	if err != nil {
		return renderErr(stdout, err)
	}
	_, _, ok, err := project.FindMarker(cwd)
	if err != nil {
		return renderErr(stdout, err)
	}
	if !ok {
		return renderErr(stdout, &axi.Error{
			Msg:  "this folder is not in a jig project: no jig.yaml marker in it or above it",
			Code: "NOT_IN_PROJECT",
			Help: []string{
				"Run it from inside a project, or make one with `jig init`",
				"`jig setup --here` sets up a repository that has no marker, or `jig setup --global` does it for every project",
			},
		})
	}
	if !p.anyPiece() {
		p.artifacts, p.byDefault = true, true
	}
	return runSetup(e, "attach", p, stdout, stdin)
}
