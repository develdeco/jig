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

// TestCommandTableFlagsMatchRegistration checks that each command's table
// flags, hidden ones included, match the names and usage it really registers.
// Every command runs with a valid positional and an unknown flag, so it fails
// in fs.Parse right after registering its flags and before doing any work.
func TestCommandTableFlagsMatchRegistration(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

	captured := map[string]*flag.FlagSet{}
	prev := newFlagSetHook
	newFlagSetHook = func(name string, fs *flag.FlagSet) { captured[name] = fs }
	t.Cleanup(func() { newFlagSetHook = prev })

	invoke := func(args ...string) {
		Main(append(append([]string{}, args...), "--nonexistent-flag-xyz"), io.Discard, strings.NewReader(""))
	}

	invoke("init")
	invoke("ticket", "new")
	invoke("solve", "T-1")
	invoke("run", "T-1")
	invoke("requeue", "T-1")
	invoke("gate", "T-1")
	invoke("publish", "T-1")
	invoke("status", "T-1")
	invoke("validate", "T-1")
	invoke("skills", "install")
	invoke("version")

	for _, c := range commandTable {
		if c.Name == "_screen" {
			// _screen reads a hook call on stdin; it never builds a
			// flag.FlagSet, so there is nothing to capture or compare.
			if c.Flags != nil {
				t.Errorf("_screen has table Flags but registers no flag.FlagSet")
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
				t.Errorf("command %q: commandTable has --%s but it is not registered on the real FlagSet", c.Name, name)
				continue
			}
			if gu != usage {
				t.Errorf("command %q: --%s usage mismatch:\n  table: %q\n  real:  %q", c.Name, name, usage, gu)
			}
		}
		for name := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("command %q: real FlagSet has --%s but commandTable does not", c.Name, name)
			}
		}
	}

	for key := range captured {
		found := false
		for _, c := range commandTable {
			if flagSetName(c.Name) == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("newFlagSet(%q) has no commandTable entry", key)
		}
	}
}

// TestPerCommandHelpFlag checks that "-h"/"--help" prints the command's flags
// and exits 0, with or without its positional argument.
func TestPerCommandHelpFlag(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())

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
			code := Main(c.args, &buf, strings.NewReader(""))
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
