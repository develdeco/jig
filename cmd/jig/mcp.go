package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mcp"
	"github.com/develdeco/jig/internal/project"
)

// The usage lines of `jig mcp`: the help row and the FlagSet carry the same
// text, which the help parity test compares.
const (
	mcpStoreUsage   = "store every tool works on (default: the one the working directory is in)"
	mcpProjectUsage = "project every tool works on, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"mcp", "serve jig's commands to an agent harness as an MCP server on stdio", []flagSpec{
			{"store", mcpStoreUsage, false},
			{"project", mcpProjectUsage, false},
		}, false},
		Order: 370,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdMCP(e, args, stdout, stdin)
		},
	})
}

// cmdMCP implements `jig mcp [--store path] [--project name]`: a Model
// Context Protocol server on stdin and stdout, for a harness to start. Its
// tools (here, next, context, claim, ask, answer, note, observe, status) each
// run the command of that name in this process, in the directory the server
// was started in, and return what it printed. With --store or --project every
// tool works on that store, whatever the directory.
//
// Stdout carries the protocol and nothing else, so an error after the server
// starts goes to stderr. The server ends when the client closes its input
// (status 0), or at the first interrupt (status 130).
func cmdMCP(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "mcp")
	storeFlag := fs.String("store", "", mcpStoreUsage)
	projectFlag := fs.String("project", "", mcpProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if len(positionals) > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("jig mcp takes no arguments, got %q", positionals[0]), Code: "VALIDATION_ERROR"})
	}
	store, err := e.abs(*storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	tools := mcp.Tools(mcp.Options{Run: mcpRunner(e), Pin: mcpPin(e, store, *projectFlag)})
	info, _ := debug.ReadBuildInfo()
	srv := mcp.NewServer(mcp.Info{Name: "jig", Version: formatVersion(info), Instructions: mcp.Instructions}, tools)
	if e.stdinIsTerminal(stdin) {
		fmt.Fprintln(os.Stderr, "jig mcp: serving MCP on stdio; an MCP client starts this, for example `claude mcp add jig -- jig mcp`")
	}
	err = srv.Serve(e.ctx, stdin, stdout)
	switch {
	case e.ctx.Err() != nil:
		return exitInterrupted
	case err != nil:
		fmt.Fprintf(os.Stderr, "jig mcp: %v\n", err)
		return 1
	}
	return 0
}

// mcpRunner runs a tool's command line through run, in this process, under
// the context of the call, and returns what it printed.
func mcpRunner(e env) mcp.Runner {
	return func(ctx context.Context, argv []string, stdin string) (string, int) {
		var out bytes.Buffer
		call := e
		call.ctx = ctx
		code := run(call, argv, &out, strings.NewReader(stdin))
		return out.String(), code
	}
}

// mcpPin returns the flags that hold a command to the store the server was
// given: --project to a command that takes it, else --store (the path given,
// or the one the machine mapping holds for the project) to a command that
// takes that. A command with neither (observe) is not pinned. The mapping is
// read at each call, so a project initialized after the server started is
// found.
func mcpPin(e env, store, projectName string) func(verb string) ([]string, error) {
	return func(name string) ([]string, error) {
		v, ok := verbNamed(name)
		if !ok || (store == "" && projectName == "") {
			return nil, nil
		}
		has := func(flag string) bool {
			for _, f := range v.Spec.Flags {
				if f.Name == flag {
					return true
				}
			}
			return false
		}
		var pins []string
		if projectName != "" && has("project") {
			pins = append(pins, "--project", projectName)
		}
		if has("store") && (store != "" || (projectName != "" && !has("project"))) {
			path := store
			if path == "" {
				var err error
				if path, err = mappedStore(e, projectName); err != nil {
					return nil, err
				}
			}
			pins = append(pins, "--store", path)
		}
		return pins, nil
	}
}

// mappedStore is the store path the machine mapping holds for a project.
func mappedStore(e env, projectName string) (string, error) {
	jigHome, err := e.jigHome()
	if err != nil {
		return "", err
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return "", err
	}
	mp, ok := machine[projectName]
	if !ok || mp.Store == "" {
		return "", fmt.Errorf("no project %q in the machine mapping", projectName)
	}
	return mp.Store, nil
}
