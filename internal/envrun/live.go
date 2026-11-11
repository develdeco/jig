package envrun

import "sync"

// The oracles a jig process has running, so that stopping jig ends them. A
// process that is told to stop (SIGINT, SIGTERM or SIGHUP, or on Windows any
// end) must not leave the tests it started running: a builder's shell that
// killed a foreground `jig gate` once left its whole suite going, and a second
// gate started another beside it.
//
// Windows ends an oracle's tree with the job object it runs in
// (guard_windows.go), which also covers jig being terminated outright. Unix
// has no such tie: an oracle runs in its own process group (so a signal sent
// to jig's group does not reach it), and EndAll, called from jig's signal
// handler, ends every live group before jig exits. A SIGKILL of jig cannot be
// handled, and a process group does not die with its leader's parent, so a
// Unix jig killed that way leaves its oracle running; Linux's Pdeathsig
// reaches only the shell, not the test runner it started, so it is not used.
var live struct {
	mu    sync.Mutex
	next  int
	ends  map[int]func()
	ended bool
}

// register records an end function for a running oracle and returns the call
// that forgets it. Once EndAll has run, a new oracle is ended at once: jig is
// going down and starts nothing it will not stop.
func register(end func()) (forget func()) {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.ended {
		end()
		return func() {}
	}
	if live.ends == nil {
		live.ends = map[int]func(){}
	}
	id := live.next
	live.next++
	live.ends[id] = end
	return func() {
		live.mu.Lock()
		defer live.mu.Unlock()
		delete(live.ends, id)
	}
}

// EndAll ends every oracle this process is running, and every one it starts
// after, which is ended as it starts. It is for the handler of the signal that
// stops jig.
func EndAll() { endLive(true) }

// endLive ends the live oracles; final also makes later ones end at once.
func endLive(final bool) {
	live.mu.Lock()
	ends := make([]func(), 0, len(live.ends))
	for _, end := range live.ends {
		ends = append(ends, end)
	}
	if final {
		live.ended = true
	}
	live.mu.Unlock()
	for _, end := range ends {
		end()
	}
}
