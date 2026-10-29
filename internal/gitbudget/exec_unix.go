//go:build unix

package main

import (
	"fmt"
	"io"
	"syscall"
)

// execTest replaces this process with the test binary, so go test's own
// signals and timeout reach the binary directly and no process sits
// between them.
func execTest(path string, argv, env []string, stderr io.Writer) int {
	err := syscall.Exec(path, argv, env)
	fmt.Fprintf(stderr, "gitbudget: run %s: %v\n", path, err)
	return 1
}
