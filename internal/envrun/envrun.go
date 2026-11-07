// Package envrun runs a manifest env class through its up/check/down
// lifecycle: it allocates a free port, substitutes {ticket} and {port} into
// each command, and runs them, as it runs an oracle, through sh on every OS
// (Sh).
package envrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

// Shell runs cmd through sh (Sh) in dir, inheriting the current process
// environment (ChildEnv: without RecordDirEnv).
func Shell(cmd, dir string) error {
	c, err := shCommand(context.Background(), cmd, dir, nil)
	if err != nil {
		return err
	}
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
// record into that run's directory. git and gh run no scenarios and may
// inherit it.
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

// ErrTimedOut is what a command the wedge guard killed for outliving its limit
// matches under errors.Is. A kill is read from this, never from the error's
// text, which embeds the command and so can hold any words.
var ErrTimedOut = errors.New("did not finish within its limit")

// TimedOutError is the error of a command killed for outliving Limit.
type TimedOutError struct {
	Cmd   string
	Limit time.Duration
}

func (e *TimedOutError) Error() string {
	return fmt.Sprintf("shell %q: %s", e.Cmd, e.Reason())
}

// Reason is the guard's own words, without the command: "did not finish
// within <limit>".
func (e *TimedOutError) Reason() string { return "did not finish within " + e.Limit.String() }

// Is reports a TimedOutError as ErrTimedOut.
func (e *TimedOutError) Is(target error) bool { return target == ErrTimedOut }

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
	c, err := shCommand(ctx, cmd, dir, env)
	if err != nil {
		return "", err
	}
	NewProcessGroup(c)
	c.Cancel = func() error { return KillTree(c) }
	c.WaitDelay = 10 * time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), &TimedOutError{Cmd: cmd, Limit: limit}
	}
	if err != nil {
		return string(out), fmt.Errorf("shell %q: %w", cmd, err)
	}
	return string(out), nil
}

// Sh is the sh every command of this package runs through, on every OS, so
// an oracle or an env class's command reads the same wherever jig runs it.
// Off Windows it is the sh on PATH. On Windows it is Git for Windows'
// bin\sh.exe, the launcher that gives a command Git Bash's environment
// (/mingw64/bin and /usr/bin first on its PATH), as an agent session's own
// shell there has it; it is found from the git on PATH, which an install
// keeps in its cmd, bin, mingw64\bin or mingw64\libexec\git-core directory.
// Without it, the sh on PATH. With neither, the error says how to get one.
func Sh() (string, error) {
	return findSh(runtime.GOOS, exec.LookPath, isFile)
}

// gitDepth is how far above the directory of the git on PATH findSh looks
// for bin\sh.exe: three, the depth of mingw64\libexec\git-core in a Git for
// Windows install.
const gitDepth = 3

// findSh is Sh on goos, with lookPath finding a program on PATH and isFile
// saying whether a path is a regular file.
func findSh(goos string, lookPath func(string) (string, error), isFile func(string) bool) (string, error) {
	if goos == "windows" {
		if git, err := lookPath("git"); err == nil {
			dir := filepath.Dir(git)
			for range gitDepth + 1 {
				if sh := filepath.Join(dir, "bin", "sh.exe"); isFile(sh) {
					return sh, nil
				}
				dir = filepath.Dir(dir)
			}
		}
	}
	if sh, err := lookPath("sh"); err == nil {
		return sh, nil
	}
	if goos == "windows" {
		return "", errors.New(`jig runs oracles and env class commands with sh, and found none: no bin\sh.exe of Git for Windows beside the git on PATH, and no sh on PATH; install Git for Windows, or put an sh on PATH`)
	}
	return "", errors.New("jig runs oracles and env class commands with sh, and found no sh on PATH")
}

// isFile reports whether path names a regular file.
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// commandEnv carries a command to sh, which runs shScript to evaluate it.
// Through the environment a command reaches sh exactly as written on every
// OS. On the command line it would not on Windows, where the runtime of
// Git's sh parses its command line by rules of its own before sh reads it
// (two backslashes in single quotes came out as one).
const commandEnv = "JIG_SHELL_COMMAND"

// shScript is what sh runs: the command in commandEnv, with commandEnv unset
// first, so neither the command nor what it starts sees it.
const shScript = `eval "unset ` + commandEnv + `; $` + commandEnv + `"`

// shCommand is cmd ready to run through sh in dir: its environment is the
// inherited one (ChildEnv), then env, then commandEnv, so neither can stand
// in for the command.
func shCommand(ctx context.Context, cmd, dir string, env []string) (*exec.Cmd, error) {
	sh, err := Sh()
	if err != nil {
		return nil, fmt.Errorf("shell %q: %w", cmd, err)
	}
	c := exec.CommandContext(ctx, sh, "-c", shScript)
	c.Dir = dir
	c.Env = append(append(ChildEnv(c), env...), commandEnv+"="+cmd)
	return c, nil
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
