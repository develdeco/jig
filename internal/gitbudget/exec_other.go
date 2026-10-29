//go:build !unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// execTest runs the test binary as a child, where the OS has no exec, and
// returns its exit code.
func execTest(path string, argv, env []string, stderr io.Writer) int {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	default:
		fmt.Fprintf(stderr, "gitbudget: run %s: %v\n", path, err)
		return 1
	}
}
