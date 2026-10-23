package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"map", "map a repo of the project to a clone on this machine", []flagSpec{
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 302,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdMap(e, args, stdout)
		},
		Probe: []string{"repo=path"},
	})
}

// cmdMap implements `jig map <repo>=<path>`: an explicit entry in the machine
// map for a repo of the project, for a clone jig cannot recognize by itself
// (a repo with no remote, so no origin to match). It replaces the repo's
// entry if there is one.
func cmdMap(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "map")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	arg, err := exactlyOne(positionals, "jig map", "<repo>=<path>")
	if err != nil {
		return renderErr(stdout, err)
	}
	name, path, ok := splitOnce(arg, '=')
	if !ok || name == "" || path == "" {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("jig map takes <repo>=<path>, got %q", arg), Code: "VALIDATION_ERROR"})
	}
	dir, err := e.abs(path)
	if err != nil {
		return renderErr(stdout, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("%s is not a directory", dir), Code: "VALIDATION_ERROR"})
	}

	st, cfg, _, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	var declared []string
	found := false
	for _, r := range cfg.Repos {
		declared = append(declared, r.Name())
		found = found || r.Name() == name
	}
	if !found {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("project %s declares no repo %q (it declares: %s)", cfg.Name, name, strings.Join(declared, ", ")),
			Code: "VALIDATION_ERROR",
		})
	}
	err = project.UpdateMachine(jigHome, func(m map[string]project.MachineProject) error {
		mp := m[cfg.Name]
		if mp.Store == "" {
			mp.Store = st.Root
		}
		clones := make(map[string]string, len(mp.Clones)+1)
		for k, v := range mp.Clones {
			clones[k] = v
		}
		clones[name] = dir
		mp.Clones = clones
		m[cfg.Name] = mp
		return nil
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("map", [][2]string{{"project", cfg.Name}, {"repo", name}, {"path", dir}}),
		axi.Help(fmt.Sprintf("Run `jig here` in %s to check it", dir)),
	)
	return 0
}
