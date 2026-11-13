package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The rows a git process that names no caller or no test is counted under.
const (
	unattributed = "(unattributed)"
	otherTest    = "(other)"
)

// attribution is who started a package's git processes: how many each Go
// function (caller) and each test started. Every process is counted once in
// each map, so both sum to total, the number of trace files.
type attribution struct {
	callers map[string]int
	tests   map[string]int
	total   int
}

// traceHead is what a trace file's head names: the variables gitx sets when
// git traces (JIG_GIT_CALLER and JIG_GIT_DIR), recorded by git as def_param
// events when its config lists them under trace2.envVars.
type traceHead struct {
	caller, dir string
}

// readTraceHead reads the head of the trace file at path, up to the point the
// def_param events end (the first region_enter, exit or atexit event), and
// never the rest. A file git is still writing, or cut short, yields what its
// head held so far.
func readTraceHead(path string) (traceHead, error) {
	f, err := os.Open(path)
	if err != nil {
		return traceHead{}, err
	}
	defer f.Close()
	var h traceHead
	haveCaller, haveDir := false, false
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var ev struct {
				Event string `json:"event"`
				Param string `json:"param"`
				Value string `json:"value"`
			}
			if json.Unmarshal(line, &ev) == nil {
				switch ev.Event {
				case "region_enter", "exit", "atexit":
					return h, nil
				case "def_param":
					switch ev.Param {
					case "JIG_GIT_CALLER":
						h.caller, haveCaller = ev.Value, true
					case "JIG_GIT_DIR":
						h.dir, haveDir = ev.Value, true
					}
				}
			}
			if haveCaller && haveDir {
				return h, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return h, nil
		}
		if err != nil {
			return h, err
		}
	}
}

// testPrefixes start the name Go gives a test, fuzz test, example or
// benchmark, and so the directory t.TempDir makes for it.
var testPrefixes = []string{"Test", "Fuzz", "Example", "Benchmark"}

// testNameFromDir derives the test that ran in dir: the nearest path
// component that starts with a test prefix, trailing digits removed (t.TempDir
// appends random digits to the test's name, cut to 40 characters). A directory
// with no such component is "(other)". The name is approximate: two tests
// whose names agree in their first 40 characters share it.
func testNameFromDir(dir string) string {
	parts := strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' })
	for i := len(parts) - 1; i >= 0; i-- {
		for _, p := range testPrefixes {
			if strings.HasPrefix(parts[i], p) {
				return strings.TrimRight(parts[i], "0123456789")
			}
		}
	}
	return otherTest
}

// attribute reads every trace file in dir and counts the processes by caller
// and by test. A directory that was never made holds none. A file that cannot
// be read counts as unattributed, so the total stays the file count that
// countTraces gives.
func attribute(dir string) (attribution, error) {
	a := attribution{callers: map[string]int{}, tests: map[string]int{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	for _, e := range entries {
		a.total++
		h, _ := readTraceHead(filepath.Join(dir, e.Name()))
		if h.caller == "" {
			a.callers[unattributed]++
			a.tests[unattributed]++
			continue
		}
		a.callers[h.caller]++
		a.tests[testNameFromDir(h.dir)]++
	}
	return a, nil
}
