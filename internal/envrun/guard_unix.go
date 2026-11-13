//go:build !windows

package envrun

import (
	"os/exec"
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
	g.sam = startSampler(g.c.Process.Pid)
	g.forget = register(func() { g.stopped.Store(true); _ = KillTree(g.c) })
	return nil
}

// usage is what the run's tree used, called once c has been waited for and
// before release. The CPU time is that of every descendant the shell waited
// for (wait4); one handed to init when its parent ended is left out. The peak
// is the largest sum of resident sets the sampler saw in the group.
func (g *guard) usage() Usage {
	var u Usage
	if g.sam != nil {
		if peak := g.sam.finish(); peak > 0 {
			u.PeakBytes, u.Basis = peak, BasisSampled
		}
	}
	if ps := g.c.ProcessState; ps != nil {
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
