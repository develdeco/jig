// Package envrun runs a manifest env class through its up/check/down
// lifecycle: it allocates a free port, substitutes {ticket} and {port} into
// each command, and shells them out via the platform's command
// interpreter.
package envrun

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/develdeco/jig/manifest"
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
// inheriting the current process environment: `cmd /C` on Windows, `sh -c`
// elsewhere.
func Shell(cmd, dir string) error {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Dir = dir
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

// substitute replaces {ticket} and {port} placeholders in cmd.
func substitute(cmd, ticket string, port int) string {
	cmd = strings.ReplaceAll(cmd, "{ticket}", ticket)
	cmd = strings.ReplaceAll(cmd, "{port}", strconv.Itoa(port))
	return cmd
}

// allocatePort asks the OS for a free TCP port by binding to :0 and
// immediately releasing it.
func allocatePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
