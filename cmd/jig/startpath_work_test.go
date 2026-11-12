package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// startPathRig is the fixture clone on its ticket's branch, plus five more
// open tickets each bound to its own branch holding one commit of work, and
// six more empty branches, so work done per open ticket or per branch shows
// as growth, as on a real store.
func startPathRig(t *testing.T) (*fixture.Fixture, env) {
	t.Helper()
	fx, e, _ := observeRig(t)
	branchOf := regexp.MustCompile(`(?m)^\s*branch:\s*(\S+)`)
	idOf := regexp.MustCompile(`(?m)^\s*id:\s*(\S+)`)
	for i := 1; i <= 5; i++ {
		out, code := runMain(t, e, "", "ticket", "new", "--title", fmt.Sprintf("Open ticket number %d", i))
		if code != 0 {
			t.Fatalf("ticket new: exit %d, output %q", code, out)
		}
		m := idOf.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("ticket new printed no id: %q", out)
		}
		next, code := runMain(t, e, "", "next", m[1])
		b := branchOf.FindStringSubmatch(next)
		if code != 0 || b == nil {
			t.Fatalf("next %s: exit %d, no branch in %q", m[1], code, next)
		}
		for _, args := range [][]string{{"checkout", "-q", "-b", b[1], "jig/" + fx.Ticket}} {
			if _, err := gitx.Run(fx.RepoDir, args...); err != nil {
				t.Fatal(err)
			}
		}
		file := fmt.Sprintf("work-%d.txt", i)
		if err := os.WriteFile(filepath.Join(fx.RepoDir, file), []byte("work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", "work " + file}} {
			if _, err := gitx.Run(fx.RepoDir, args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 1; i <= 6; i++ {
		if _, err := gitx.Run(fx.RepoDir, "branch", fmt.Sprintf("jig/extra-%d", i), "jig/"+fx.Ticket); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitx.Run(fx.RepoDir, "checkout", "-q", "jig/"+fx.Ticket); err != nil {
		t.Fatal(err)
	}
	return fx, e
}

// measureWork runs call and returns the git programs it started, counted as
// CI's budget counts them (one trace file per git process in a fresh empty
// GIT_TRACE2_EVENT directory), and the in-process reads gitx answered.
func measureWork(t *testing.T, call func()) (processes int, reads int64) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_TRACE2_EVENT", dir)
	before := gitx.InProcessReads()
	call()
	reads = gitx.InProcessReads() - before
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(files), reads
}

// checkWork fails when processes or reads exceed their ceilings, printing both.
func checkWork(t *testing.T, name string, processes int, reads int64, maxProcesses int, maxReads int64) {
	t.Helper()
	t.Logf("%s: %d git processes (ceiling %d), %d in-process reads (ceiling %d)", name, processes, maxProcesses, reads, maxReads)
	if processes > maxProcesses {
		t.Errorf("%s started %d git processes, ceiling %d", name, processes, maxProcesses)
	}
	if reads > maxReads {
		t.Errorf("%s made %d in-process reads, ceiling %d", name, reads, maxReads)
	}
}

// hereMaxProcesses and hereMaxReads bound what `jig here` costs on the
// startPathRig fixture: git programs started (trace files) and in-process
// reads (gitx.InProcessReads). Each is the count measured on main plus 2; a
// change that adds work on purpose raises it.
const (
	hereMaxProcesses       = 10
	hereMaxReads     int64 = 5
)

// Not parallel: t.Setenv and the process-wide read counter need it.
func TestHereWorkStaysUnderItsCeiling(t *testing.T) {
	_, e := startPathRig(t)
	var out string
	var code int
	processes, reads := measureWork(t, func() { out, code = runMain(t, e, "", "here") })
	if code != 0 {
		t.Fatalf("jig here: exit %d, output %q", code, out)
	}
	checkWork(t, "jig here", processes, reads, hereMaxProcesses, hereMaxReads)
}
