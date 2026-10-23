package gitx

import (
	"bytes"
	"errors"
	"io/fs"
)

// RunInput is Run with stdin written to git's standard input: the way to hand
// git an object to store (`hash-object -w --stdin`) or a tree to build
// (`mktree`) without a temporary file. Like Run it returns trimmed stdout, and
// on failure an error that carries git's stderr.
func RunInput(dir string, stdin []byte, args ...string) (string, error) {
	return RunInputEnv(dir, nil, stdin, args...)
}

// RunInputEnv is RunInput with env appended to the process environment, as
// RunEnv does: a caller pinning a commit date for `commit-tree` passes it
// here.
func RunInputEnv(dir string, env []string, stdin []byte, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := runInput(dir, env, stdin, &stdout, &stderr, args)
	if errors.Is(err, fs.ErrNotExist) && dirExists(dir) && forgetGit() {
		// git itself vanished from the path run reused: search PATH again,
		// as run does for the calls without input.
		stdout.Reset()
		stderr.Reset()
		err = runInput(dir, env, stdin, &stdout, &stderr, args)
	}
	if err != nil {
		return "", callError(args, stderr.String(), err)
	}
	return string(bytes.TrimSpace(stdout.Bytes())), nil
}

// runInput is one attempt of RunInputEnv: the same command run builds
// ("-c maintenance.auto=false" ahead of args, the repository variables
// dropped from the inherited environment), with stdin on its standard input.
func runInput(dir string, env []string, stdin []byte, stdout, stderr *bytes.Buffer, args []string) error {
	cmd := gitCommand(append([]string{"-c", "maintenance.auto=false"}, args...))
	cmd.Dir = dir
	cmd.Env = append(inheritedEnv(), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
