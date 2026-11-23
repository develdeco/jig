package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// The digests of a ticket standing at each step, as `jig next` words them.

func briefWaiting() *digestInfo {
	d := task("brief", false, "current|drafted · waiting for your approval", "pending|after the brief", "pending|after the build",
		"pending|after a clean review", "pending|after the publish")
	d.Phases[0].NeedsYou = true
	return d
}

func buildUnder() *digestInfo {
	return task("build", true, "done|approved by Ada", "current|1 of 2 green", "pending|after the build",
		"pending|after a clean review", "pending|after the publish")
}

func reviewReady() *digestInfo {
	return task("review", true, "done|approved by Ada", "done|2 slices green", "current|ready for a gate round",
		"pending|after a clean review", "pending|after the publish")
}

func reviewOpen() *digestInfo {
	return task("review", false, "done|approved by Ada", "done|2 slices green", "current|round 1 is open",
		"pending|after a clean review", "pending|after the publish")
}

func publishReady() *digestInfo {
	return task("publish", true, "done|approved by Ada", "done|2 slices green", "done|round 1 clean",
		"current|ready: approve it, push the branch and open the pull request", "pending|after the publish")
}

// quietRigAt is a rig of ticket id over a store whose roster lists the ticket
// with the digest d, which the rig keeps as it is: a key that runs a verb leaves
// the ticket reading the same, so the line it leaves stays.
func quietRigAt(t *testing.T, id string, d *digestInfo) *rig {
	t.Helper()
	g := newRig(t, id)
	phases := make([]map[string]any, len(d.Phases))
	for i, p := range d.Phases {
		phases[i] = map[string]any{"name": p.Name, "state": p.State, "text": p.Text, "needs_you": p.NeedsYou}
	}
	wire := map[string]any{"kind": d.Kind, "current": d.Current, "ready": d.Ready, "behind": d.Behind, "closed": d.Closed, "phases": phases}
	if sl := d.Slices; sl != nil {
		wire["slices"] = map[string]any{"total": sl.Total, "green": sl.Green, "building": sl.Building, "queued": sl.Queued, "stalled": sl.Stalled, "env_blocked": sl.EnvBlocked}
	}
	var listed []map[string]any
	for _, other := range []string{"JIG-1", "JIG-2"} {
		if other != id {
			listed = append(listed, map[string]any{"id": other, "title": "Another ticket", "state": "ready"})
		}
	}
	listed = append(listed, map[string]any{"id": id, "title": "Config loader", "state": "ready", "digest": wire})
	out, err := json.Marshal(map[string]any{"tickets": listed})
	if err != nil {
		t.Fatal(err)
	}
	g.r.answer("next --json --store /s", string(out)+"\n", 0)
	g.pin(id, d)
	return g
}

func landStop() *digestInfo {
	return task("land", false, "done|approved by Ada", "done|2 slices green", "done|round 1 clean",
		"done|pull request opened", "current|waits to land")
}

// can1 is a rig of JIG-2, the ticket the digest d stands, with its status read
// (the asks of its round are there).
func can1(t *testing.T, d *digestInfo) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	if d.Current == "land" {
		// Published: its asks were decided and applied.
		g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
		g.feed(jig2()...)
	} else {
		g.feed(jig2Mid()...)
	}
	g.pin("JIG-2", d)
	g.syncDigests()
	return g
}

// TestCanReadsTheDigest: each action is ok or not by what the roster's digest
// says of the ticket and what the status says is waiting, and says why when it
// is not.
func TestCanReadsTheDigest(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		d    *digestInfo
		act  action
		ok   bool
		why  string
	}{
		{"gate at the review", reviewReady(), actGate, true, ""},
		{"gate with a round open", reviewOpen(), actGate, false, "the review is not ready: round 1 is open"},
		{"gate before the build is done", buildUnder(), actGate, false, "the review is not ready: after the build"},
		{"gate after the review", publishReady(), actGate, false, "the review is done: round 1 clean"},
		{"publish when ready", publishReady(), actPublish, true, ""},
		{"publish at the review", reviewReady(), actPublish, false, "the publish is not ready: after a clean review"},
		{"publish when behind", func() *digestInfo { d := publishReady(); d.Behind = true; return d }(), actPublish, false, ""},
		{"gate of a closed ticket", func() *digestInfo { d := reviewReady(); d.Closed, d.Ready = "covered by another", false; return d }(), actGate, false, "JIG-2 is closed"},
		{"keep with asks", reviewOpen(), actKeep, true, ""},
		{"dismiss with asks", reviewOpen(), actDismiss, true, ""},
		{"answer with no question", reviewOpen(), actAnswer, false, "no question is waiting for an answer"},
		{"requeue with no stalled slice", buildUnder(), actRequeue, false, "no slice is stalled or env-blocked"},
		{"requeue with a stalled slice", func() *digestInfo { d := buildUnder(); d.Slices = &digestSlices{Total: 2, Stalled: 1}; return d }(), actRequeue, true, ""},
		{"requeue with an env-blocked slice", func() *digestInfo { d := buildUnder(); d.Slices = &digestSlices{Total: 2, EnvBlocked: 1}; return d }(), actRequeue, true, ""},
		{"approve a waiting brief", briefWaiting(), actApprove, true, ""},
		{"approve an approved brief", buildUnder(), actApprove, false, "the brief is not waiting for your approval"},
		{"gate at the landing stop", landStop(), actGate, false, `landing is said to the session: "Land JIG-2"`},
		{"publish at the landing stop", landStop(), actPublish, false, `landing is said to the session: "Land JIG-2"`},
	} {
		g := can1(t, c.d)
		ok, why := g.m.can(c.act)
		if ok != c.ok || (c.why != "" && why != c.why) || (ok && why != "") {
			t.Errorf("%s: can(%s) = %v, %q, want %v, %q", c.name, c.act, ok, why, c.ok, c.why)
		}
		if !ok && why == "" {
			t.Errorf("%s: a refusal with no reason", c.name)
		}
	}

	// An open question is an answer to give.
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	if ok, why := g.m.can(actAnswer); !ok {
		t.Errorf("answer with a question open: %q", why)
	}
	if ok, why := g.m.can(actKeep); ok || why != "no ask is waiting for a decision" {
		t.Errorf("keep with no ask = %v, %q", ok, why)
	}

	// A ticket the roster has not read says so, and offers nothing.
	g = can1(t, reviewReady())
	g.m.w.tickets["JIG-2"].digest = nil
	for _, a := range []action{actAnswer, actKeep, actDismiss, actGate, actPublish, actRequeue, actApprove} {
		if ok, why := g.m.can(a); ok || why != readingText {
			t.Errorf("can(%s) before the roster read = %v, %q, want %q", a, ok, why, readingText)
		}
	}

	// No ticket is under the cursor on the project's list until the cursor is on one.
	main := newRig(t, "")
	if ok, why := main.m.can(actGate); ok || why != "select a ticket first" {
		t.Errorf("can(gate) with no ticket = %v, %q", ok, why)
	}
}

// footerKeys are the lines of the hint line of a frame: those under the last
// rule.
func footerKeys(frame string) string {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "───") {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return ""
}

// TestHintsOfferOnlyWhatRuns: a ticket at its brief, build, review, publish and
// landing offers the keys that can run there, with esc back and q quit first and
// q quit never alone on a line.
func TestHintsOfferOnlyWhatRuns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		d            *digestInfo
		has, hasNone []string
	}{
		{"brief", briefWaiting(), []string{"5 land", "t tail"}, []string{"g gate", "p publish", "a answer"}},
		{"build", buildUnder(), []string{"5 land"}, []string{"g gate", "p publish", "a answer"}},
		{"review", reviewReady(), []string{"g gate", "k keep", "d dismiss"}, []string{"p publish", "a answer"}},
		{"publish", publishReady(), []string{"p publish", "k keep"}, []string{"g gate", "a answer"}},
		{"landing", landStop(), []string{`say "Land JIG-2"`, "5 land"}, []string{"g gate", "p publish", "a answer", "k keep", "d dismiss"}},
	} {
		g := can1(t, c.d)
		hint := footerKeys(g.view())
		if !strings.HasPrefix(strings.TrimSpace(hint), "esc back · q quit") {
			t.Errorf("%s: the hint line does not start with esc back and q quit:\n%s", c.name, hint)
		}
		for _, h := range c.has {
			if !strings.Contains(hint, h) {
				t.Errorf("%s: the hint line lacks %q:\n%s", c.name, h, hint)
			}
		}
		for _, h := range c.hasNone {
			if strings.Contains(hint, h) {
				t.Errorf("%s: the hint line offers %q, which cannot run:\n%s", c.name, h, hint)
			}
		}
	}

	// The project's list lists q quit first, and it is never alone on a line.
	g := mainRig(t)
	for _, down := range []int{0, 1, 2} {
		hint := footerKeys(g.view())
		if !strings.HasPrefix(strings.TrimSpace(hint), "q quit") {
			t.Errorf("the project's hint line does not start with q quit:\n%s", hint)
		}
		for _, l := range strings.Split(hint, "\n") {
			if strings.TrimSpace(l) == "q quit" {
				t.Errorf("q quit stands alone on a line:\n%s", hint)
			}
		}
		_ = down
		g.press("down")
	}

	// Gate is listed for the ticket under the cursor on the project's list.
	g = mainRig(t)
	g.pin("JIG-2", reviewReady())
	g.syncDigests()
	g.press("down")
	if hint := footerKeys(g.view()); !strings.Contains(hint, "g gate") {
		t.Errorf("the project's list does not offer the gate of a ticket at its review:\n%s", hint)
	}
	g.press("up")
	if hint := footerKeys(g.view()); strings.Contains(hint, "g gate") {
		t.Errorf("the project's list offers a gate for the ticket under the cursor, which is building:\n%s", hint)
	}
}

// TestAKeyThatCannotRunSaysWhy: a key whose action cannot run runs nothing and
// puts the reason in the notice, the one the hints would have listed it by.
func TestAKeyThatCannotRunSaysWhy(t *testing.T) {
	t.Parallel()
	g := can1(t, buildUnder())
	before := len(g.r.lines())
	for key, want := range map[string]string{
		"g": "the review is not ready: after the build",
		"p": "the publish is not ready: after a clean review",
		"a": "no question is waiting for an answer",
	} {
		g.press(key)
		if g.m.notice != want {
			t.Errorf("%s: the notice is %q, want %q", key, g.m.notice, want)
		}
		if g.m.ask != nil || g.m.input != nil {
			t.Errorf("%s opened a question for a key that cannot run", key)
		}
		if got := len(g.r.lines()); got != before {
			t.Errorf("%s ran %q", key, g.r.lines()[before:])
		}
		if !strings.Contains(g.view(), want) {
			t.Errorf("%s: the notice is not on the screen:\n%s", key, g.view())
		}
	}

	// The same key, once it can run, runs.
	g = can1(t, reviewReady())
	before = len(g.r.lines())
	g.press("g")
	if got := g.r.lines(); len(got) != before+1 || !strings.HasPrefix(got[before], "gate JIG-2") {
		t.Errorf("g at the review ran %q", got[before:])
	}
}

// TestLandingStopHintSaysThePrompt: at the landing stop the hint line carries the
// person's word for the session where the merge keys would be, and no key does
// anything.
func TestLandingStopHintSaysThePrompt(t *testing.T) {
	t.Parallel()
	g := can1(t, landStop())
	want := `say "Land JIG-2"`
	if hint := footerKeys(g.view()); !strings.Contains(hint, want) {
		t.Fatalf("the hint line does not say the word:\n%s", hint)
	}
	before := len(g.r.lines())
	for _, key := range []string{"a", "k", "d", "g", "p"} {
		g.press(key)
		if g.m.notice != `landing is said to the session: "Land JIG-2"` {
			t.Errorf("%s: the notice is %q", key, g.m.notice)
		}
		if g.m.ask != nil || g.m.input != nil || len(g.r.lines()) != before {
			t.Errorf("%s did something at the landing stop: commands %q", key, g.r.lines()[before:])
		}
	}

	// The land screen says it too, and lists no key to merge.
	g.press("5")
	if g.m.Screen() != "land" {
		t.Fatalf("on %s, want land", g.m.Screen())
	}
	if hint := footerKeys(g.view()); !strings.Contains(hint, want) || strings.Contains(hint, "gate") {
		t.Errorf("the land screen's hint line:\n%s", hint)
	}

	// A ticket that is not at the landing stop says no such word.
	g = can1(t, publishReady())
	if hint := footerKeys(g.view()); strings.Contains(hint, "Land ") {
		t.Errorf("a ticket at its publish says the landing word:\n%s", hint)
	}
}
