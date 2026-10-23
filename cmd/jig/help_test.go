package main

import (
	"bytes"
	"flag"
	"io"
	"strings"
	"testing"
)

// handParsedFlags lists, per command, the table flags extractAnswer parses by
// hand ("--answer <qid> <text>" takes two values) before any FlagSet exists.
var handParsedFlags = map[string]map[string]bool{
	"solve": {"answer": true},
	"run":   {"answer": true},
}

// TestCommandTableFlagsMatchRegistration checks that each registered verb's
// help-row flags, hidden ones included, match the names and usage it really
// registers. Every verb runs with its Probe positionals and an unknown flag,
// so it fails in fs.Parse right after registering its flags and before doing
// any work. A verb that never builds a FlagSet (NoFlagSet) has no Flags to
// compare and is not run.
func TestCommandTableFlagsMatchRegistration(t *testing.T) {
	t.Parallel()
	captured := map[string]*flag.FlagSet{}
	e := testEnv(t.TempDir())
	e.newFlagSetHook = func(name string, fs *flag.FlagSet) { captured[name] = fs }

	verbs := registeredVerbs()
	for _, v := range verbs {
		if v.NoFlagSet {
			continue
		}
		args := append(strings.Fields(v.Spec.Name), v.Probe...)
		run(e, append(args, "--nonexistent-flag-xyz"), io.Discard, strings.NewReader(""))
	}

	for _, v := range verbs {
		c := v.Spec
		if v.NoFlagSet {
			// A hook verb such as _screen reads a call on stdin; it never
			// builds a flag.FlagSet, so there is nothing to capture or
			// compare.
			if c.Flags != nil {
				t.Errorf("%s is NoFlagSet but has table Flags", c.Name)
			}
			continue
		}

		key := flagSetName(c.Name)
		fs, ok := captured[key]
		if !ok {
			t.Errorf("command %q: no flag.FlagSet captured (looked for newFlagSet(%q))", c.Name, key)
			continue
		}

		want := map[string]string{}
		for _, f := range c.Flags {
			if handParsedFlags[c.Name][f.Name] {
				continue
			}
			want[f.Name] = f.Usage
		}
		got := map[string]string{}
		fs.VisitAll(func(fl *flag.Flag) {
			got[fl.Name] = fl.Usage
		})

		for name, usage := range want {
			gu, ok := got[name]
			if !ok {
				t.Errorf("command %q: its help row has --%s but it is not registered on the real FlagSet", c.Name, name)
				continue
			}
			if gu != usage {
				t.Errorf("command %q: --%s usage mismatch:\n  table: %q\n  real:  %q", c.Name, name, usage, gu)
			}
		}
		for name := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("command %q: real FlagSet has --%s but its help row does not", c.Name, name)
			}
		}
	}

	for key := range captured {
		found := false
		for _, v := range verbs {
			if !v.NoFlagSet && flagSetName(v.Spec.Name) == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("newFlagSet(%q) has no registered verb", key)
		}
	}
}

// TestPerCommandHelpFlag checks that "-h"/"--help" prints the command's flags
// and exits 0, with or without its positional argument.
func TestPerCommandHelpFlag(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())

	cases := []struct {
		name string
		args []string
		want string // substring the output must contain
	}{
		{"gate --help with a ticket", []string{"gate", "T-1", "--help"}, "flags{gate}"},
		{"gate -h with no ticket", []string{"gate", "-h"}, "flags{gate}"},
		{"ticket new -h", []string{"ticket", "new", "-h"}, "flags{ticket}"},
		{"version --help", []string{"version", "--help"}, "flags{version}"},
		{"ticket -h", []string{"ticket", "-h"}, "flags{ticket}"},
		{"skills --help", []string{"skills", "--help"}, "flags{skills}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			code := run(e, c.args, &buf, strings.NewReader(""))
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; output:\n%s", code, buf.String())
			}
			if strings.Contains(buf.String(), "VALIDATION_ERROR") {
				t.Fatalf("output unexpectedly contains VALIDATION_ERROR:\n%s", buf.String())
			}
			if !strings.Contains(buf.String(), c.want) {
				t.Fatalf("output missing %q:\n%s", c.want, buf.String())
			}
		})
	}
}

// TestGateHelpSaysItReadsLocalSessions: a reviewer round with no brief and
// no explicit intent reads the operator's local Claude Code transcripts and
// has a model summarize the best match, so the command's own help - the
// environment is the source for CLI behavior - says so, in the flags a
// person reaches for to state an intent themselves.
func TestGateHelpSaysItReadsLocalSessions(t *testing.T) {
	t.Parallel()
	got := strings.Join(flagsBlockFor("gate"), "\n")
	for _, want := range []string{"local Claude Code sessions", "--intent or --doc"} {
		if !strings.Contains(got, want) {
			t.Errorf("jig gate's flag help does not mention %q:\n%s", want, got)
		}
	}
}
