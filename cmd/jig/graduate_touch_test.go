package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// TestGraduateTiesTheMintingSessionToTheTask: the session that graduates a
// chart works on each task it mints from then on, as for jig ticket new: a
// session-touch line names every task (not a multi-task) in the machine
// ledger, and a graduation outside a session writes none.
func TestGraduateTiesTheMintingSessionToTheTask(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"s-plan", ""} {
		home := t.TempDir()
		vars := map[string]string{"JIG_HOME": home, "HOME": home, "USERPROFILE": home}
		if session != "" {
			vars[locate.SessionEnv] = session
			if err := ledger.StartSession(home, time.Time{}, ledger.Registration{ID: session, Harness: "claude", Model: "claude-sonnet-5"}); err != nil {
				t.Fatal(err)
			}
		}
		clone := filepath.Join(t.TempDir(), "clone")
		newTestOriginClone(t, clone)
		e := envFrom(vars)
		var buf bytes.Buffer
		if code := run(e, []string{"chart", "new", "plan", "--title", "Plan", "--need", "A need", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
			t.Fatalf("jig chart new: exit %d\n%s", code, buf.String())
		}
		st, err := store.Open(clone)
		if err != nil {
			t.Fatal(err)
		}
		chart := "issue: DEMO-1\ntickets:\n  - title: Group\n    kind: multi-task\n  - title: Work\n    under: \"#1\"\n"
		if err := os.WriteFile(st.ChartFile("plan"), []byte(chart), 0o644); err != nil {
			t.Fatal(err)
		}
		buf.Reset()
		if code := run(e, []string{"graduate", "plan", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
			t.Fatalf("jig graduate: exit %d\n%s", code, buf.String())
		}
		lines, err := ledger.Read(home)
		if err != nil {
			t.Fatal(err)
		}
		var touched []string
		for _, l := range lines {
			if l.Event == journal.EventSessionTouch {
				if l.Session != session || l.Harness != "claude" || l.Model != "claude-sonnet-5" {
					t.Errorf("session-touch line = %+v, want %s with the harness and model it started with", l, session)
				}
				touched = append(touched, l.Ticket)
			}
		}
		switch {
		case session == "" && len(touched) != 0:
			t.Errorf("a graduation outside a session wrote touches for %v", touched)
		case session != "" && (len(touched) != 1 || touched[0] != "DEMO-3"):
			t.Errorf("session-touch tickets = %v, want only the task DEMO-3 (not the multi-task DEMO-2)", touched)
		}
	}
}

// TestGraduateTiesTheMintingSessionWarningGoesToTheEnvStderr: a ledger that
// cannot record the tie warns on the env's stderr, not the process's, and not
// on stdout; the graduation still succeeds.
func TestGraduateTiesTheMintingSessionWarningGoesToTheEnvStderr(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// The ledger lives in <home>/ledger: a file there cannot be read or written.
	if err := os.WriteFile(filepath.Join(home, "ledger"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := envFrom(map[string]string{"JIG_HOME": home, "HOME": home, "USERPROFILE": home, locate.SessionEnv: "s-x"})
	var warn bytes.Buffer
	e.stderr = &warn
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)
	var buf bytes.Buffer
	if code := run(e, []string{"chart", "new", "plan", "--title", "Plan", "--need", "A need", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s", code, buf.String())
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.ChartFile("plan"), []byte("issue: DEMO-1\ntickets:\n  - title: Work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if code := run(e, []string{"graduate", "plan", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig graduate: exit %d\n%s", code, buf.String())
	}
	if !strings.Contains(warn.String(), "could not record that this session works on DEMO-2") || strings.Contains(buf.String(), "could not record") {
		t.Errorf("stderr = %q, stdout = %q, want the warning on the env's stderr only", warn.String(), buf.String())
	}
}
