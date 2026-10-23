package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"ignore", "stop jig tracking a directory (a clone of a known project it should leave alone)", []flagSpec{
			{"remove", "track the directory again instead", false},
		}, false},
		Order: 303,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdIgnore(e, args, stdout)
		},
		Probe: []string{"."},
	})
}

// cmdIgnore implements `jig ignore <path>`: the directory, and everything
// under it, is no longer found by jig.yaml, the machine map or an origin
// remote, so `jig here` there says nothing and no verb maps it. `--store`
// still names a store explicitly.
func cmdIgnore(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "ignore")
	remove := fs.Bool("remove", false, "track the directory again instead")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	path, err := exactlyOne(positionals, "jig ignore", "path")
	if err != nil {
		return renderErr(stdout, err)
	}
	jigHome, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	dir, err := e.abs(path)
	if err != nil {
		return renderErr(stdout, err)
	}
	status := ""
	if *remove {
		removed, err := project.Unignore(jigHome, dir)
		if err != nil {
			return renderErr(stdout, err)
		}
		status = "not ignored"
		if removed {
			status = "tracked again"
		}
	} else {
		added, err := project.Ignore(jigHome, dir)
		if err != nil {
			return renderErr(stdout, err)
		}
		status = "already ignored"
		if added {
			status = "ignored"
		}
	}
	axi.Render(stdout,
		axi.KV("ignore", [][2]string{{"path", dir}, {"status", status}}),
		axi.Help(fmt.Sprintf("`jig ignore %s --remove` tracks it again", path)),
	)
	return 0
}
