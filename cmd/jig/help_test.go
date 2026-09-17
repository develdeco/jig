package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// handParsedFlags lists, per command, the table flags extractAnswer parses by
// hand ("--answer <qid> <text>" takes two values) before any FlagSet exists.
var handParsedFlags = map[string]map[string]bool{
	"solve": {"answer": true},
	"run":   {"answer": true},
}

// flagSetKeyFor maps a commandTable command name to the name it passes to
// newFlagSet, for commands whose flag set has a different name (a
// subcommand's flags are registered under "<cmd> <sub>").
func flagSetKeyFor(cmdName string) string {
	switch cmdName {
	case "ticket":
		return "ticket new"
	case "skills":
		return "skills install"
	default:
		return cmdName
	}
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

		key := flagSetKeyFor(c.Name)
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
			if flagSetKeyFor(c.Name) == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("newFlagSet(%q) has no commandTable entry", key)
		}
	}
}

// TestBareHelpMatchesREADME checks that README.md's bare `jig` output block
// matches what the binary prints.
func TestBareHelpMatchesREADME(t *testing.T) {
	var buf bytes.Buffer
	if code := Main(nil, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig (bare) exit code = %d", code)
	}
	want := strings.TrimRight(buf.String(), "\n")

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	readmePath := filepath.Join(filepath.Dir(file), "..", "..", "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := strings.ReplaceAll(string(data), "\r\n", "\n")

	marker := "`jig` with no arguments prints this:\n\n```\n"
	start := strings.Index(readme, marker)
	if start == -1 {
		t.Fatal(`README.md: missing "jig` + "`" + ` with no arguments prints this:" fenced block`)
	}
	start += len(marker)
	end := strings.Index(readme[start:], "\n```")
	if end == -1 {
		t.Fatal("README.md: unterminated fenced block after the bare-jig marker")
	}
	got := readme[start : start+end]

	if got != want {
		t.Fatalf("README.md's bare `jig` block is stale; want it replaced with:\n\n%s", want)
	}
}
