package e2e

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// stopGrace is how long Stop gives a jig process, once its stdin is closed,
// to exit on its own before killing it.
const stopGrace = 2 * time.Second

// jigProc is a jig subprocess a test talks to while it runs: a protocol
// server it writes requests to and reads replies from, or a stream it reads
// lines of until it has seen enough. Start one with startJig. It does not
// record the run (runJig and runJigIn do): there is no single output to show.
//
// Line, Write and CloseStdin are for the one goroutine of the test that owns
// the process; Stop may be called from anywhere, any number of times.
type jigProc struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu      sync.Mutex
	lines   []string      // every complete stdout line so far
	next    int           // index of the first line Line has not returned
	partial []byte        // stdout after the last newline
	stdoutB bytes.Buffer  // all of stdout, for Stop's result
	stderrB bytes.Buffer  // all of stderr
	eof     bool          // the process has exited and its stdout is fully read
	wake    chan struct{} // signalled when lines or eof change

	closeOnce sync.Once
	done      chan struct{} // closed when the process has exited and been waited for
	stopOnce  sync.Once
	result    jigResult
}

// startJig starts the built jig binary with cwd and args in the process
// environment plus env's variables, with a pipe for its stdin, and returns
// the handle. The process is stopped when the test ends if the test did not
// stop it. A start that fails fails the test.
func startJig(t *testing.T, env jigEnv, cwd string, args ...string) *jigProc {
	t.Helper()
	if env.home == "" {
		// An empty JIG_HOME makes jig fall back to the real home's jig dir.
		t.Fatal("startJig: the jigEnv names no jig home")
	}
	p := &jigProc{t: t, wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.cmd = exec.Command(jigBinary, args...)
	p.cmd.Dir = cwd
	p.cmd.Env = env.environ()
	p.cmd.Stdout = stdoutSink{p}
	p.cmd.Stderr = lockedWriter{&p.mu, &p.stderrB}
	// A process that left a child holding its pipes must not hold Wait.
	p.cmd.WaitDelay = stopGrace
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatalf("startJig: stdin pipe: %v", err)
	}
	p.stdin = stdin
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("startJig: start %s %s: %v", jigBinary, strings.Join(args, " "), err)
	}
	go func() {
		_ = p.cmd.Wait()
		p.mu.Lock()
		if len(p.partial) > 0 { // output that ended without a newline is a last line
			p.lines = append(p.lines, string(p.partial))
			p.partial = nil
		}
		p.eof = true
		p.mu.Unlock()
		p.signal()
		close(p.done)
	}()
	t.Cleanup(func() { p.Stop() })
	return p
}

// signal wakes a Line waiting for output.
func (p *jigProc) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Line returns the next line of the process's stdout that Line has not
// returned yet, without its line ending, waiting up to d for it to arrive. It
// reports false when none arrived in time or the process exited with nothing
// left to read.
func (p *jigProc) Line(d time.Duration) (string, bool) {
	p.t.Helper()
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		p.mu.Lock()
		if p.next < len(p.lines) {
			line := p.lines[p.next]
			p.next++
			p.mu.Unlock()
			return line, true
		}
		eof := p.eof
		p.mu.Unlock()
		if eof {
			return "", false
		}
		select {
		case <-p.wake:
		case <-timer.C:
			return "", false
		}
	}
}

// Write sends b to the process's stdin and fails the test if it cannot.
func (p *jigProc) Write(b []byte) {
	p.t.Helper()
	if _, err := p.stdin.Write(b); err != nil {
		p.t.Fatalf("jigProc.Write: %v", err)
	}
}

// CloseStdin ends the process's input, as a client hanging up does. It may be
// called more than once.
func (p *jigProc) CloseStdin() {
	p.closeOnce.Do(func() { p.stdin.Close() })
}

// Stop ends the process and returns what it printed and how it exited. It
// closes stdin and gives the process stopGrace to exit by itself, then kills
// it; a process that had already exited keeps its own exit code. The result
// holds all of stdout, the lines Line returned included. Stop is idempotent:
// later calls return the first call's result.
func (p *jigProc) Stop() jigResult {
	p.stopOnce.Do(func() {
		p.CloseStdin()
		select {
		case <-p.done:
		case <-time.After(stopGrace):
			p.cmd.Process.Kill()
			<-p.done
		}
		code := -1
		if p.cmd.ProcessState != nil {
			code = p.cmd.ProcessState.ExitCode()
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.result = jigResult{Stdout: p.stdoutB.String(), Stderr: p.stderrB.String(), Code: code}
	})
	return p.result
}

// stdoutSink receives a process's stdout: it keeps all of it and splits it
// into lines for Line.
type stdoutSink struct{ p *jigProc }

func (s stdoutSink) Write(b []byte) (int, error) {
	p := s.p
	p.mu.Lock()
	p.stdoutB.Write(b)
	p.partial = append(p.partial, b...)
	for {
		i := bytes.IndexByte(p.partial, '\n')
		if i < 0 {
			break
		}
		p.lines = append(p.lines, strings.TrimSuffix(string(p.partial[:i]), "\r"))
		p.partial = p.partial[i+1:]
	}
	p.mu.Unlock()
	p.signal()
	return len(b), nil
}

// lockedWriter appends to a buffer under a mutex.
type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(b)
}
