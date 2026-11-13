//go:build !windows

package envrun

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
)

// guard ties an oracle's process tree to this process (see live.go): on Unix
// the process group, ended by KillTree on a timeout and by EndAll when jig is
// stopped.
type guard struct {
	stopFlag
	c      *exec.Cmd
	forget func()
	sam    *sampler
	// killed is set when the bound ended the group: the kill reaps nothing,
	// so the shell's wait4 usage is the shell's own and not the tree's.
	killed atomic.Bool
}

// newGuard prepares c, before it starts, to run in a group of its own and to
// end that group when its context ends.
func newGuard(c *exec.Cmd) *guard {
	NewProcessGroup(c)
	g := &guard{c: c}
	c.Cancel = func() error { g.killed.Store(true); return KillTree(c) }
	return g
}

// start starts c and registers its group as live.
func (g *guard) start() error {
	if err := g.c.Start(); err != nil {
		return err
	}
	g.sam = startSampler(g.c.Process.Pid)
	g.forget = register(func() { g.stopped.Store(true); _ = KillTree(g.c) })
	return nil
}

// usage is what the run's tree used, called once c has been waited for and
// before release. The CPU time is that of every descendant the shell waited
// for (wait4); one handed to init when its parent ended is left out. A group
// the bound killed has no CPU figure: its descendants were killed with the
// shell and never waited for, so what wait4 reads is near zero, and a near-zero
// figure told as measured would be wrong. The peak
// is the largest sum of resident sets the sampler saw in the group.
func (g *guard) usage() Usage {
	var u Usage
	if g.sam != nil {
		if peak := g.sam.finish(); peak > 0 {
			u.PeakBytes, u.Basis = peak, BasisSampled
		}
	}
	if ps := g.c.ProcessState; ps != nil && !g.killed.Load() {
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru != nil {
			u.CPU = time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond +
				time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
		}
	}
	return u
}

// release is called once c has been waited for: its group is no longer live.
func (g *guard) release() {
	if g.sam != nil {
		g.sam.finish()
	}
	if g.forget != nil {
		g.forget()
	}
}
