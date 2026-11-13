//go:build windows

package envrun

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// guard ties an oracle's process tree to this process (see live.go): a job
// object that kills on close, the shell in it before it can run, so the whole
// tree ends when the job's last handle closes, which includes jig being
// terminated. With no job (it could not be made, or the shell could not be
// put in it) the tree is ended by taskkill (KillTree) on a timeout, as it was.
type guard struct {
	stopFlag
	c      *exec.Cmd
	forget func()

	// mu guards job and done: the end functions run from jig's signal
	// handler while the run may be finishing, and release closes the job.
	// An end after release does nothing: its handle and pid are no longer
	// the run's.
	mu   sync.Mutex
	job  windows.Handle // 0: no job, KillTree is the end
	done bool
}

// newGuard prepares c, before it starts, to start suspended and be put in a
// job object that kills on close, and to end the job when its context ends.
func newGuard(c *exec.Cmd) *guard {
	g := &guard{c: c}
	if job, err := newKillOnCloseJob(); err == nil {
		g.job = job
		c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	}
	c.Cancel = g.end
	return g
}

// end ends the run's tree: the job's processes, or with no job the tree under
// the shell. After release it does nothing.
func (g *guard) end() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return nil
	}
	if g.job != 0 {
		return windows.TerminateJobObject(g.job, 1)
	}
	return KillTree(g.c)
}

// newKillOnCloseJob makes a job object that terminates its processes when its
// last handle closes.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := setKillOnClose(job, true); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// setKillOnClose sets whether closing job's last handle terminates its
// processes.
func setKillOnClose(job windows.Handle, kill bool) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if kill {
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

// start starts c suspended, puts it in the job, and lets it run, so it cannot
// start a child that escapes the job. A process that cannot be put in the job
// runs without one.
func (g *guard) start() error {
	g.mu.Lock()
	if err := g.c.Start(); err != nil {
		if g.job != 0 {
			windows.CloseHandle(g.job)
			g.job = 0
		}
		g.mu.Unlock()
		return err
	}
	if g.job != 0 {
		if err := g.assign(); err != nil {
			windows.CloseHandle(g.job)
			g.job = 0
		}
		resumeProcess(uint32(g.c.Process.Pid))
	}
	g.mu.Unlock()
	g.forget = register(func() {
		g.stopped.Store(true)
		_ = g.end()
	})
	return nil
}

// assign puts the started process in the job.
func (g *guard) assign() error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(g.c.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(g.job, h)
}

// resumeProcess resumes the threads of the suspended process pid: a process
// started suspended has the one.
func resumeProcess(pid uint32) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var e windows.ThreadEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Thread32First(snap, &e); err == nil; err = windows.Thread32Next(snap, &e) {
		if e.OwnerProcessID != pid {
			continue
		}
		if th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, e.ThreadID); err == nil {
			windows.ResumeThread(th)
			windows.CloseHandle(th)
		}
	}
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which x/sys does
// not define: times are in 100 ns units.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// usage is what the run's tree used, read from the job before release closes
// it: the user plus kernel time of every process that was ever in it, ended
// ones included, and the job's peak committed memory. With no job nothing is
// measured.
func (g *guard) usage() Usage {
	g.mu.Lock()
	defer g.mu.Unlock()
	var u Usage
	if g.job == 0 {
		return u
	}
	var acct jobAccounting
	if err := windows.QueryInformationJobObject(g.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil); err == nil {
		u.CPU = time.Duration(acct.TotalUserTime+acct.TotalKernelTime) * 100
	}
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(g.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil); err == nil && ext.PeakJobMemoryUsed > 0 {
		u.PeakBytes, u.Basis = uint64(ext.PeakJobMemoryUsed), BasisJob
	}
	return u
}

// release is called once c has been waited for. The run is over, so what the
// oracle left behind is not ended with it, as before: the job stops killing
// on close and is let go.
func (g *guard) release() {
	if g.forget != nil {
		g.forget()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.done = true
	if g.job != 0 {
		_ = setKillOnClose(g.job, false)
		windows.CloseHandle(g.job)
		g.job = 0
	}
}
