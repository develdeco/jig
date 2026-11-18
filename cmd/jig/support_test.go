package main

import (
	"io"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// Main dispatches one CLI invocation against the process's own environment
// and working directory, and returns the process exit code: the binary's entry
// without a subprocess. A test that gives the run an environment of its own
// calls run.
func Main(args []string, stdout io.Writer, stdin io.Reader) int {
	return run(processEnv(), args, stdout, stdin)
}

// RenderStatus is the status block of ticket as `jig status` writes it, over the
// registered sections.
func RenderStatus(st *store.Store, ticket string) (string, error) {
	return renderStatus(st, ticket, statusSections())
}

// renderStatus is RenderStatus over the given sections: each one's blocks
// follow the status blocks and come before the help line.
func renderStatus(st *store.Store, ticket string, sections []statusSection) (string, error) {
	return renderStatusWith(st, ticket, sections, derivePhase)
}

// resolveStore resolves the store, project config and this machine's clone
// mapping the way a command that allows no older schema does.
func resolveStore(e env, storeFlag string, stdout io.Writer) (*store.Store, project.Config, project.MachineProject, string, error) {
	return resolveStoreChecked(e, storeFlag, stdout, false)
}
