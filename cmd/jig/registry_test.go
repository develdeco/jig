package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

// noopRun is a Run for a verb a test registers only to look it up.
func noopRun(env, []string, io.Writer, io.Reader) int { return 0 }

// namedVerb is a registrable verb whose Run says which verb it is through its
// exit code, so a lookup test can tell which one it got.
func namedVerb(name string, code int) verb {
	return verb{
		Spec: cmdSpec{Name: name, Summary: name},
		Run:  func(env, []string, io.Writer, io.Reader) int { return code },
	}
}

// TestLookupVerbTwoWord pins that a two-word verb name is tried before the
// one-word name its first word makes, that the arguments after the name
// come back, and that a name nothing registered is not found. It uses a
// registry of its own, not the binary's.
func TestLookupVerbTwoWord(t *testing.T) {
	t.Parallel()
	r := newVerbRegistry()
	r.register(namedVerb("review", 1))
	r.register(namedVerb("review context", 2))
	r.register(namedVerb("ticket", 3))

	cases := []struct {
		name     string
		args     []string
		wantVerb string
		wantRest []string
		wantOK   bool
	}{
		{"two words win", []string{"review", "context", "R-1", "--json"}, "review context", []string{"R-1", "--json"}, true},
		{"two words with nothing after", []string{"review", "context"}, "review context", []string{}, true},
		{"one word when the second is not a subverb", []string{"review", "done"}, "review", []string{"done"}, true},
		{"one word alone", []string{"ticket"}, "ticket", []string{}, true},
		{"a flag after a one-word verb", []string{"ticket", "-h"}, "ticket", []string{"-h"}, true},
		{"the first word of a two-word name is its own verb", []string{"review"}, "review", []string{}, true},
		{"unknown", []string{"bogus", "context"}, "", nil, false},
		{"no args", nil, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, rest, ok := r.lookup(c.args)
			if ok != c.wantOK || v.Spec.Name != c.wantVerb {
				t.Fatalf("lookup(%q) = verb %q, found %v, want verb %q, found %v", c.args, v.Spec.Name, ok, c.wantVerb, c.wantOK)
			}
			if ok && !slices.Equal(rest, c.wantRest) {
				t.Errorf("lookup(%q) rest = %q, want %q", c.args, rest, c.wantRest)
			}
		})
	}

	// With no "review" verb, "review" alone is unknown but "review context"
	// is still found.
	only := newVerbRegistry()
	only.register(namedVerb("review context", 2))
	if _, _, ok := only.lookup([]string{"review"}); ok {
		t.Errorf("lookup([review]) found a verb, want none: only the two-word name is registered")
	}
	if v, _, ok := only.lookup([]string{"review", "context"}); !ok || v.Run(env{}, nil, nil, nil) != 2 {
		t.Errorf("lookup([review context]) = found %v, want the two-word verb", ok)
	}
}

// TestLookupVerbFindsTheBinarysVerbs checks the package-level lookup against
// the verbs this binary registers: a verb with a subcommand of its own is
// found by its first word, with the subcommand left in the arguments.
func TestLookupVerbFindsTheBinarysVerbs(t *testing.T) {
	t.Parallel()
	v, rest, ok := lookupVerb([]string{"ticket", "new", "--title", "x"})
	if !ok || v.Spec.Name != "ticket" || !slices.Equal(rest, []string{"new", "--title", "x"}) {
		t.Fatalf("lookupVerb(ticket new ...) = %q %q found %v, want ticket with [new --title x]", v.Spec.Name, rest, ok)
	}
	if _, _, ok := lookupVerb([]string{"no-such-verb"}); ok {
		t.Errorf("lookupVerb(no-such-verb) found a verb")
	}
}

// TestRegisterVerbRefusesWhatCannotWork pins the registration panics: a
// duplicate name, a name that is not one or two plain words, a verb that
// cannot run, and a NoFlagSet verb that still lists flags.
func TestRegisterVerbRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()
	mustPanic := func(t *testing.T, want string, f func()) {
		t.Helper()
		defer func() {
			got := recover()
			if got == nil {
				t.Fatalf("no panic, want one mentioning %q", want)
			}
			if msg, _ := got.(string); !strings.Contains(msg, want) {
				t.Fatalf("panic = %v, want it to mention %q", got, want)
			}
		}()
		f()
	}

	r := newVerbRegistry()
	r.register(namedVerb("alpha", 0))
	mustPanic(t, "already registered", func() { r.register(namedVerb("alpha", 0)) })
	for _, bad := range []string{"", " alpha", "alpha  beta", "alpha beta gamma", "alpha "} {
		mustPanic(t, "not one word or two", func() { r.register(namedVerb(bad, 0)) })
	}
	mustPanic(t, "no Run", func() { r.register(verb{Spec: cmdSpec{Name: "norun"}}) })
	mustPanic(t, "NoFlagSet", func() {
		r.register(verb{Spec: cmdSpec{Name: "hook", Flags: []flagSpec{{"x", "y", false}}}, Run: noopRun, NoFlagSet: true})
	})
}

// TestRegisteredVerbsSortByOrderThenName pins the order verbs come out in,
// and that the verbs which predate the registry keep their old help order
// inside their 100..240 band.
func TestRegisteredVerbsSortByOrderThenName(t *testing.T) {
	t.Parallel()

	r := newVerbRegistry()
	for _, v := range []struct {
		name  string
		order int
	}{{"zeta", 5}, {"beta", 7}, {"alpha", 7}, {"first", 1}} {
		x := namedVerb(v.name, 0)
		x.Order = v.order
		r.register(x)
	}
	var got []string
	for _, v := range r.verbs() {
		got = append(got, v.Spec.Name)
	}
	if want := []string{"first", "zeta", "alpha", "beta"}; !slices.Equal(got, want) {
		t.Errorf("verbs() order = %q, want %q: by Order, then by Name", got, want)
	}

	// The verbs that were in the command table before the registry, in the
	// order it held them. A later ticket may delete one; the rest keep this
	// relative order.
	legacy := []string{"init", "ticket", "graduate", "solve", "run", "requeue", "gate", "publish", "status", "validate", "version", "skills", "trackers", "store", "_screen"}
	var seen []string
	for _, v := range registeredVerbs() {
		if slices.Contains(legacy, v.Spec.Name) {
			seen = append(seen, v.Spec.Name)
			if v.Order < 100 || v.Order > 240 {
				t.Errorf("verb %q has Order %d, want it in the 100..240 band of the verbs that predate the registry", v.Spec.Name, v.Order)
			}
		}
	}
	var want []string
	for _, name := range legacy {
		if slices.Contains(seen, name) {
			want = append(want, name)
		}
	}
	if !slices.Equal(seen, want) {
		t.Errorf("pre-registry verbs sort as %q, want %q", seen, want)
	}
}

// TestRunRefusesAnUnknownVerb pins the refusal of a verb nothing registered:
// a VALIDATION_ERROR naming the first word, exit 2, and the pointer to the
// command list.
func TestRunRefusesAnUnknownVerb(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	code := run(testEnv(t.TempDir()), []string{"bogus", "ticket"}, &buf, strings.NewReader(""))
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	for _, want := range []string{`unknown command "bogus"`, "code: VALIDATION_ERROR", "Run `jig` with no arguments to see the command list"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}
