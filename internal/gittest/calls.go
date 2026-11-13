package gittest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitCall is one git process a traced call started: its command line and the
// directory it ran in (gitx names it when git traces; "" when it did not).
type GitCall struct {
	Argv []string
	Dir  string
}

// GitCalls is GitArgv with each process's directory, from the JIG_GIT_DIR
// variable gitx hands git when tracing, and only the processes jig started: the
// ones git started in turn (an upload-pack, a pack-objects) are left out. The
// same rule applies: the test must
// not be parallel.
func GitCalls(t testing.TB, fn func()) []GitCall {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	fn()
	f, err := os.Open(trace)
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
			if strings.Contains(ev.Sid, "/") {
				continue // a process git started, named under its parent's
			}
			calls[ev.Sid] = &GitCall{Argv: ev.Argv}
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
