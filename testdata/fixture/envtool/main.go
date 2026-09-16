// Command envtool is a tiny stdlib-only stand-in for a real development
// environment's lifecycle script. The jig fixture's "rig" environment class
// drives it so envrun has something deterministic and cross-platform to run
// in tests.
//
// Usage:
//
//	envtool up [--fail] <port> <file>   writes "port=<port>" to file
//	envtool check <file>                exit 0 iff file exists
//	envtool down <file>                 removes file (ok if already absent)
package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fail("usage: envtool up [--fail] <port> <file> | check <file> | down <file>")
	}

	switch args[0] {
	case "up":
		runUp(args[1:])
	case "check":
		runCheck(args[1:])
	case "down":
		runDown(args[1:])
	default:
		fail("unknown command: " + args[0])
	}
}

func runUp(args []string) {
	shouldFail := false
	if len(args) > 0 && args[0] == "--fail" {
		shouldFail = true
		args = args[1:]
	}
	if len(args) != 2 {
		fail("usage: envtool up [--fail] <port> <file>")
	}
	port, file := args[0], args[1]

	if shouldFail {
		// Exit before writing, so the caller can tell up never completed.
		os.Exit(1)
	}
	if err := os.WriteFile(file, []byte("port="+port), 0o644); err != nil {
		fail(err.Error())
	}
}

func runCheck(args []string) {
	if len(args) != 1 {
		fail("usage: envtool check <file>")
	}
	if _, err := os.Stat(args[0]); err != nil {
		os.Exit(1)
	}
}

func runDown(args []string) {
	if len(args) != 1 {
		fail("usage: envtool down <file>")
	}
	if err := os.Remove(args[0]); err != nil && !os.IsNotExist(err) {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "envtool: "+msg)
	os.Exit(2)
}
