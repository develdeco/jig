// Command ghstub is a fake `gh` binary used only by tracker's github-adapter
// tests. It never talks to GitHub: it records every invocation's argv to
// $GH_STUB_LOG (one JSON array per line) and answers from a small canned
// set of responses keyed off the subcommand, tracking an incrementing issue
// counter in $GH_STUB_STATE so repeated "issue create" calls mint distinct
// numbers.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	logArgs(os.Getenv("GH_STUB_LOG"), os.Args)

	statePath := os.Getenv("GH_STUB_STATE")
	args := os.Args[1:]

	switch {
	case len(args) >= 2 && args[0] == "issue" && args[1] == "create":
		n := nextCounter(statePath)
		fmt.Printf("https://github.example/owner/repo/issues/%d\n", n)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		n := currentCounter(statePath)
		fmt.Printf(`{"data":{"repository":{"issue":{"id":"NODE%d"},"parent":{"id":"NODEP"}}}}`+"\n", n)
	default:
		fmt.Println("{}")
	}
}

func logArgs(path string, args []string) {
	if path == "" {
		return
	}
	data, err := json.Marshal(args)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(data))
}

func readCounter(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func writeCounter(path string, n int) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
}

// nextCounter increments and persists the issue counter, returning the new
// value (so the first minted issue is #1).
func nextCounter(path string) int {
	n := readCounter(path) + 1
	writeCounter(path, n)
	return n
}

// currentCounter reads the issue counter without incrementing it, so a
// GraphQL resolve call after a mint reports the just-minted number.
func currentCounter(path string) int {
	return readCounter(path)
}
