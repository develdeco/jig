package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// runFunc is a verb's entry point: its arguments after the verb's own words,
// the stdout its output goes to, and its stdin. It returns the exit code.
type runFunc func(e env, args []string, stdout io.Writer, stdin io.Reader) int

// verb is one registered subcommand: its help row, where it sorts in help,
// and how to run it. Each verb registers itself from an init() in its own
// file, so adding a verb adds a file and edits none that exist.
type verb struct {
	// Spec is the verb's help row. Spec.Name is one word, or two words for a
	// verb with a subcommand of its own ("review context").
	Spec cmdSpec
	// Order sorts the verb in help and in registeredVerbs, then by Name. The
	// verbs that predate the registry hold 100..240 in their old order; each
	// later ticket has a band from 300 up (D3 300s, D8 310s, D6 320, D10
	// 330s, D4 340s, D15 350, D16 360, D14 370, D9 380, D12 390, D17 400s,
	// D5 500s, D7 900).
	Order int
	// Run executes the verb.
	Run runFunc
	// FlagSet is the name the verb gives newFlagSet; empty means Spec.Name.
	// A verb whose flags belong to its one subcommand names it here
	// ("ticket new").
	FlagSet string
	// Probe is the positional arguments the help parity test passes after
	// the verb's words, so the verb reaches its flag parse and fails there
	// on an unknown flag, before it does any work.
	Probe []string
	// NoFlagSet marks a hidden verb that never builds a FlagSet (it reads a
	// hook payload on stdin, say). Its Spec.Flags must be nil.
	NoFlagSet bool
}

// verbRegistry is a set of verbs by name. The binary has exactly one,
// defaultRegistry, filled by init() functions before main runs and only read
// after; a test that must not touch it builds one of its own.
type verbRegistry struct {
	byName map[string]verb
}

func newVerbRegistry() *verbRegistry {
	return &verbRegistry{byName: map[string]verb{}}
}

// register adds v. It panics on a malformed verb or a name already taken: a
// registration is source code, so either is a bug found the first time the
// binary or its tests start.
func (r *verbRegistry) register(v verb) {
	name := v.Spec.Name
	if words := strings.Fields(name); len(words) < 1 || len(words) > 2 || strings.Join(words, " ") != name {
		panic(fmt.Sprintf("registerVerb: name %q is not one word or two single-spaced words", name))
	}
	if v.Run == nil {
		panic(fmt.Sprintf("registerVerb: verb %q has no Run", name))
	}
	if v.NoFlagSet && v.Spec.Flags != nil {
		panic(fmt.Sprintf("registerVerb: verb %q is NoFlagSet but its Spec has Flags", name))
	}
	if _, dup := r.byName[name]; dup {
		panic(fmt.Sprintf("registerVerb: verb %q is already registered", name))
	}
	r.byName[name] = v
}

// verbs returns every registered verb, by Order and then by Name.
func (r *verbRegistry) verbs() []verb {
	out := make([]verb, 0, len(r.byName))
	for _, v := range r.byName {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b verb) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Spec.Name, b.Spec.Name)
	})
	return out
}

// lookup finds the verb args begin with and the arguments left after its
// words. A two-word name is tried before a one-word one, so "review context"
// wins over a "review" that would take "context" as its own argument.
func (r *verbRegistry) lookup(args []string) (verb, []string, bool) {
	if len(args) >= 2 {
		if v, ok := r.byName[args[0]+" "+args[1]]; ok {
			return v, args[2:], true
		}
	}
	if len(args) >= 1 {
		if v, ok := r.byName[args[0]]; ok {
			return v, args[1:], true
		}
	}
	return verb{}, nil, false
}

// defaultRegistry holds the binary's verbs.
var defaultRegistry = newVerbRegistry()

// registerVerb adds v to the binary's verbs. Each verb calls it from an
// init() in its own file; it panics when the name is already taken.
func registerVerb(v verb) { defaultRegistry.register(v) }

// registeredVerbs returns the binary's verbs sorted by Order, then by Name.
func registeredVerbs() []verb { return defaultRegistry.verbs() }

// lookupVerb finds the registered verb args begin with, and the arguments
// after its name; it tries the two-word name first.
func lookupVerb(args []string) (verb, []string, bool) { return defaultRegistry.lookup(args) }

// verbNamed returns the registered verb whose Spec.Name is name.
func verbNamed(name string) (verb, bool) {
	v, ok := defaultRegistry.byName[name]
	return v, ok
}

// commandTable lists every registered verb's help row, in help order.
func commandTable() []cmdSpec {
	vs := registeredVerbs()
	out := make([]cmdSpec, len(vs))
	for i, v := range vs {
		out[i] = v.Spec
	}
	return out
}
