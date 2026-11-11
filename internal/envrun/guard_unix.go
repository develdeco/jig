//go:build !windows

package envrun

import "os/exec"

// guard ties an oracle's process tree to this process (see live.go): on Unix
// the process group, ended by KillTree on a timeout and by EndAll when jig is
// stopped.
type guard struct {
	stopFlag
	c      *exec.Cmd
	forget func()
}

// newGuard prepares c, before it starts, to run in a group of its own and to
// end that group when its context ends.
func newGuard(c *exec.Cmd) *guard {
	NewProcessGroup(c)
	c.Cancel = func() error { return KillTree(c) }
	return &guard{c: c}
}

// start starts c and registers its group as live.
func (g *guard) start() error {
	if err := g.c.Start(); err != nil {
		return err
	}
	g.forget = register(func() { g.stopped.Store(true); _ = KillTree(g.c) })
	return nil
}

// release is called once c has been waited for: its group is no longer live.
func (g *guard) release() {
	if g.forget != nil {
		g.forget()
	}
}
