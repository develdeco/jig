package e2e

import (
	"strings"
	"testing"
)

// phaseOut is the phase block of `jig next <ticket> --json`.
type phaseOut struct {
	Phase struct {
		Name   string `json:"name"`
		Ready  bool   `json:"ready"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
		Prompt string `json:"prompt"`
		Phases []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Text  string `json:"text"`
		} `json:"phases"`
		For *struct {
			Phase   string `json:"phase"`
			Ready   bool   `json:"ready"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
			Warning string `json:"warning"`
		} `json:"for"`
	} `json:"phase"`
}

// phaseStates is the state of each of the four phases, in order, as one
// string: "dcpp" is the brief done, the build current, the rest pending.
func (p phaseOut) states() string {
	var out string
	for _, ph := range p.Phase.Phases {
		out += ph.State[:1]
	}
	return out
}

// expectPhase checks both forms of one `jig next <ticket>` answer, in text and
// in JSON: the phase the ticket is at, whether that phase can start, the code
// that says why not, the prompt that starts it and the state of each phase
// ("dcpp" is the brief done, the build current, the rest pending). The chain
// calls it at the points a ticket passes through (TestDigesterFlow); the rules
// behind each answer are pinned at their seams, internal/phase's TestDerive and
// cmd/jig's TestNextNamesThePhaseAndPrompt.
func expectPhase(t *testing.T, env jigEnv, cwd, ticket, when, phase string, ready bool, code, prompt, states string) phaseOut {
	t.Helper()
	out := nextJSON[phaseOut](t, env, cwd, ticket)
	p := out.Phase
	if p.Name != phase || p.Ready != ready || p.Code != code || p.Prompt != prompt || out.states() != states {
		t.Errorf("%s: phase=%s ready=%v code=%q prompt=%q states=%s, want %s %v %q %q %s (reason %q)",
			when, p.Name, p.Ready, p.Code, p.Prompt, out.states(), phase, ready, code, prompt, states, p.Reason)
	}
	if !ready && p.Reason == "" {
		t.Errorf("%s: not ready, and no reason says why", when)
	}
	text := runJig(t, env, cwd, "next", ticket)
	yes := map[bool]string{true: "yes", false: "no"}[ready]
	wants := []string{"phase: " + phase + "\n", "ready: " + yes + "\n"}
	if prompt != "" {
		wants = append(wants, "prompt: "+prompt+"\n")
	}
	for _, want := range wants {
		if !strings.Contains(text.Stdout, want) {
			t.Errorf("%s: jig next %s lacks %q:\n%s", when, ticket, want, text.Stdout)
		}
	}
	if !ready && !strings.Contains(text.Stdout, "code: "+code+"\n") {
		t.Errorf("%s: jig next %s lacks the code %s:\n%s", when, ticket, code, text.Stdout)
	}
	return out
}

// phaseAsked is the answer of `jig next <ticket> --for <phase>`: the phase
// asked about, which may be ahead of the ticket (a refusal with a code) or
// behind it (a warning with the prompt that moves on).
func phaseAsked(t *testing.T, env jigEnv, cwd, ticket, phase string) phaseOut {
	t.Helper()
	return nextJSON[phaseOut](t, env, cwd, ticket, "--for", phase)
}
