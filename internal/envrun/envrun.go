// Package envrun runs a manifest env class through its up/check/down
// lifecycle: it allocates a free port, substitutes {ticket} and {port} into
// each command, and shells them out via the platform's command
// interpreter.
package envrun

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/manifest"
)

// Handle is a running env class instance: the port it was given and the
// values needed to tear it back down.
type Handle struct {
	Port   int
	Class  manifest.EnvClass
	Ticket string
	Dir    string
}

// Unavailable is returned when an env class fails to come up or fails its
// health check. Policy carries the class's declared Unavailable policy
// ("defer-ci" or "" for pause) so the caller can route accordingly.
type Unavailable struct {
	Policy string
	Stage  string // "up" or "check"
	Err    error
}

// Error implements the error interface.
func (u *Unavailable) Error() string {
	return fmt.Sprintf("env %s failed (policy=%q): %v", u.Stage, u.Policy, u.Err)
}

// Unwrap exposes the underlying command failure.
func (u *Unavailable) Unwrap() error { return u.Err }

// Up allocates a free TCP port, substitutes it and ticket into c's up and
// check commands (the same values are used for down as well), and runs up
// then check in dir. Any failure best-effort tears the class back down via
// Down and returns a typed *Unavailable naming the stage that failed.
func Up(c manifest.EnvClass, ticket, dir string) (*Handle, error) {
	port, err := allocatePort()
	if err != nil {
		return nil, fmt.Errorf("envrun: allocate port: %w", err)
	}
	h := &Handle{Port: port, Class: c, Ticket: ticket, Dir: dir}

	if err := Shell(substitute(c.Up, ticket, port), dir); err != nil {
		h.bestEffortDown()
		return nil, &Unavailable{Policy: c.Unavailable, Stage: "up", Err: err}
	}
	if err := Shell(substitute(c.Check, ticket, port), dir); err != nil {
		h.bestEffortDown()
		return nil, &Unavailable{Policy: c.Unavailable, Stage: "check", Err: err}
	}
	return h, nil
}

// Down runs the class's down command, substituting the same ticket and
// port values that Up used.
func (h *Handle) Down() error {
	return Shell(substitute(h.Class.Down, h.Ticket, h.Port), h.Dir)
}

// bestEffortDown runs Down and discards any error: used when Up is already
// failing and reporting a second error would only obscure the first.
func (h *Handle) bestEffortDown() {
	_ = h.Down()
}

// Shell runs cmd through the platform's command interpreter in dir,
// inheriting the current process environment (ChildEnv: without
// RecordDirEnv): `cmd /C` on Windows, `sh -c` elsewhere.
func Shell(cmd, dir string) error {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Dir = dir
	c.Env = ChildEnv(c)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("shell %q: %s", cmd, msg)
	}
	return nil
}

// RecordDirEnv names the variable that gives an oracle run the directory its
// scenarios record into (ADR 0029). jig sets it only on a builder's green
// oracle run; unset means do not record. Because the variable is how a run
// opts in, jig never passes it on from its own environment to a command in
// this package (an oracle run, an env class's up, check and down) or to a
// headless session (internal/session), both through ChildEnv. jig can itself
// run inside a recording oracle run (it tests itself), and these must not
// record into that run's directory. git, gh and graphify run no scenarios and
// may inherit it. A
// herdr terminal session does run the project's tests, but in herdr's own
// environment, which jig does not set, so jig cannot strip it from there.
const RecordDirEnv = "JIG_RECORD_DIR"

// ChildEnv is the environment cmd's process will get, for a caller that
// builds cmd's Env from the inherited one: cmd.Environ(), which with Env still
// nil is the process environment plus the PWD os/exec sets for cmd.Dir (set
// cmd.Dir first, or the child inherits jig's own PWD, which names another
// directory), without RecordDirEnv.
func ChildEnv(cmd *exec.Cmd) []string {
	return environWithout(cmd.Environ(), runtime.GOOS, RecordDirEnv)
}

// environWithout returns environ ("NAME=value" entries) without the entries
// named in names. Names are compared case-insensitively on goos == "windows",
// where environment variable names are not case sensitive, and exactly
// elsewhere. environ is not modified.
func environWithout(environ []string, goos string, names ...string) []string {
	same := func(a, b string) bool { return a == b }
	if goos == "windows" {
		same = strings.EqualFold
	}
	out := make([]string, 0, len(environ))
next:
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		for _, name := range names {
			if same(key, name) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// ShellOutput runs cmd like Shell and returns its combined stdout and
// stderr, which a failed oracle run hands back to the session that must fix
// it. A run longer than limit is killed and reported as failed, and Wait
// stops waiting on its pipes shortly after, so a hung command cannot hold
// the caller. The kill ends the shell's whole process tree (KillTree).
func ShellOutput(cmd, dir string, limit time.Duration) (string, error) {
	return ShellOutputEnv(cmd, dir, limit, nil)
}

// ShellOutputEnv is ShellOutput with extra environment variables, each as
// "NAME=value", added to the process environment for this command alone. It
// sets them on the command, never on the process, since two runs of jig's
// loop may be in flight at once. The command inherits the process environment
// (ChildEnv: without an inherited RecordDirEnv, so a run given none does not
// record into the directory of an outer jig run), and a variable in env
// replaces an inherited one of the same name.
func ShellOutputEnv(cmd, dir string, limit time.Duration, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	c.Dir = dir
	c.Env = append(ChildEnv(c), env...)
	NewProcessGroup(c)
	c.Cancel = func() error { return KillTree(c) }
	c.WaitDelay = 10 * time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("shell %q: did not finish within %s", cmd, limit)
	}
	if err != nil {
		return string(out), fmt.Errorf("shell %q: %w", cmd, err)
	}
	return string(out), nil
}

// substitute replaces {ticket} and {port} placeholders in cmd.
func substitute(cmd, ticket string, port int) string {
	cmd = strings.ReplaceAll(cmd, "{ticket}", ticket)
	cmd = strings.ReplaceAll(cmd, "{port}", strconv.Itoa(port))
	return cmd
}

// portProbeAddr binds loopback only: the probe never exposes a port to the
// network, and Windows Firewall does not prompt for loopback listeners.
const portProbeAddr = "127.0.0.1:0"

// allocatePort asks the OS for a free TCP port by binding portProbeAddr and
// immediately releasing it.
func allocatePort() (int, error) {
	l, err := net.Listen("tcp", portProbeAddr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
