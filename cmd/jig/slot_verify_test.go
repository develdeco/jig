package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/slot"
)

// TestVerifyOfAHeavyOracleWaitsForTheProjectsSlotAndHereShowsIt: the project
// wiring end to end. A slice's oracle the manifest marks heavy, verified with
// `jig verify --wait` while another run holds the project's slot, journals
// slot-wait, shows as a waiting row in `jig here` and `jig status`, runs only
// once the slot is free, and journals slot-taken before it passes.
func TestVerifyOfAHeavyOracleWaitsForTheProjectsSlotAndHereShowsIt(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	manifestPath := filepath.Join(r.fx.RepoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	r.commit("a", "mark the oracle heavy", map[string]string{".claude/jig.yaml": string(data) + "\nheavy: [test]\n"})
	cfg, err := project.LoadStoreConfig(r.fx.StoreDir)
	if err != nil || cfg.Name == "" {
		t.Fatalf("store config = %+v, %v", cfg, err)
	}
	release, err := slot.Take(context.Background(), r.fx.Home, slot.Request{Project: cfg.Name, Who: slot.Who("OTHER", "x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := r.run("verify", "--wait")
		done <- result{out, code}
	}()

	has := func(event string) bool {
		for _, l := range r.journal() {
			if l.Event == event {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(120 * time.Second); !has(journal.EventSlotWait); {
		if time.Now().After(deadline) {
			t.Fatal("jig verify never journaled that it waits for the slot")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if has(journal.EventOracle) {
		t.Fatal("the oracle ran while another run held the slot")
	}
	wantLine := "waiting for the suite slot: OTHER/x is running its suite (1 waiting)"
	if out, code := runMain(t, r.e, "", "here"); code != 0 || !strings.Contains(out, "waiting") || !strings.Contains(out, wantLine) {
		t.Errorf("jig here (exit %d) lacks the waiting row %q:\n%s", code, wantLine, out)
	}

	release()
	released = true
	res := <-done
	if res.code != 0 {
		t.Fatalf("verify --wait exit = %d\n%s", res.code, res.out)
	}
	var order []string
	for _, l := range r.journal() {
		switch l.Event {
		case journal.EventSlotWait, journal.EventSlotTaken, journal.EventOracle:
			order = append(order, l.Event)
		}
	}
	if got := strings.Join(order, " "); got != "slot-wait slot-taken oracle" {
		t.Errorf("slot lines = %q, want slot-wait, slot-taken, then the oracle", got)
	}
	if out, _ := runMain(t, r.e, "", "here"); strings.Contains(out, "waiting for the suite slot") {
		t.Errorf("jig here still shows the wait after the slot was taken:\n%s", out)
	}
}

// TestAnUnsetMarkerSlotsMakesAProjectWaitForAnotherProjectsRun: a project
// whose marker sets no slots is measured, so its heavy verification shares one
// budget with other projects and waits while another project's run holds the
// machine. A project pinned to a count would not wait on it.
func TestAnUnsetMarkerSlotsMakesAProjectWaitForAnotherProjectsRun(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	manifestPath := filepath.Join(r.fx.RepoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	r.commit("a", "mark the oracle heavy", map[string]string{".claude/jig.yaml": string(data) + "\nheavy: [test]\n"})
	release, err := slot.Take(context.Background(), r.fx.Home, slot.Request{Project: "another-project", Who: slot.Who("OTHER", "x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	done := make(chan int, 1)
	go func() {
		_, code := r.run("verify", "--wait")
		done <- code
	}()
	has := func(event string) bool {
		for _, l := range r.journal() {
			if l.Event == event {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(120 * time.Second); !has(journal.EventSlotWait); {
		if time.Now().After(deadline) {
			t.Fatal("the verification did not wait for another project's run")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if has(journal.EventOracle) {
		t.Fatal("the oracle ran while another project's run held the machine")
	}
	release()
	released = true
	if code := <-done; code != 0 {
		t.Fatalf("verify --wait exit = %d", code)
	}
}
