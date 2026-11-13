package gittest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitCall is one git process a trace names: its command line and the
// directory it ran in (gitx names it when git traces, through the JIG_GIT_DIR
// variable; "" when it did not).
type GitCall struct {
	Argv []string
	Dir  string
	// Child says a git process started it (an upload-pack, a pack-objects),
	// not jig.
	Child bool
}

// ReadTrace reads the GIT_TRACE2_EVENT file a traced run wrote and returns its
// processes in the order they started. A file that is not there is no
// process.
func ReadTrace(t testing.TB, path string) []GitCall {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("gittest: read the git trace: %v", err)
	}
	defer f.Close()
	var order []string
	calls := map[string]*GitCall{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var ev struct {
			Event, Sid, Param, Value string
			Argv                     []string
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "start":
			calls[ev.Sid] = &GitCall{Argv: ev.Argv, Child: strings.Contains(ev.Sid, "/")}
			order = append(order, ev.Sid)
		case "def_param":
			if c := calls[ev.Sid]; c != nil && ev.Param == "JIG_GIT_DIR" {
				c.Dir = ev.Value
			}
		}
	}
	out := make([]GitCall, 0, len(order))
	for _, sid := range order {
		out = append(out, *calls[sid])
	}
	return out
}

// GitCalls runs fn with git tracing on and returns the git processes jig
// started in it, not the ones git started in turn, in the order they began.
// The same rule applies as for GitArgv: the test must not be parallel.
func GitCalls(t testing.TB, fn func()) []GitCall {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	fn()
	var out []GitCall
	for _, c := range ReadTrace(t, trace) {
		if !c.Child {
			out = append(out, c)
		}
	}
	return out
}
