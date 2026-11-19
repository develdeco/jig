package envrun

import (
	"bytes"
	"time"
)

// ShellOutput runs cmd like Shell and returns its combined stdout and stderr.
// A run longer than limit is killed and reported as failed.
func ShellOutput(cmd, dir string, limit time.Duration) (string, error) {
	return ShellOutputEnv(cmd, dir, limit, nil)
}

// ShellOutputEnv is ShellOutput with extra environment variables, each as
// "NAME=value", added to the process environment for this command alone.
func ShellOutputEnv(cmd, dir string, limit time.Duration, env []string) (string, error) {
	out, _, err := ShellOutputUsage(cmd, dir, limit, env)
	return out, err
}

// ShellOutputUsage is ShellOutputEnv that also returns what the run's process
// tree used (Usage), over ShellStream.
func ShellOutputUsage(cmd, dir string, limit time.Duration, env []string) (string, Usage, error) {
	var buf bytes.Buffer
	use, err := ShellStream(cmd, dir, limit, env, &buf)
	return buf.String(), use, err
}
