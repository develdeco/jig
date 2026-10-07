package envrun

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/manifest"
)

// existsCmd and removeCmd build platform-appropriate shell fragments so the
// marker-file test exercises the same substitution path Shell uses in
// production, without depending on a particular OS.
func existsCmd(name string) string {
	if runtime.GOOS == "windows" {
		return "if exist " + name + " (exit 0) else (exit 1)"
	}
	return "test -f " + name
}

func removeCmd(name string) string {
	if runtime.GOOS == "windows" {
		return "del " + name
	}
	return "rm -f " + name
}

// trimEOL strips the trailing line ending `echo` writes.
func trimEOL(s string) string {
	return strings.TrimRight(s, "\r\n")
}

func TestShellExitCodes(t *testing.T) {
	dir := t.TempDir()
	if err := Shell("exit 0", dir); err != nil {
		t.Fatalf("exit 0: %v", err)
	}
	if err := Shell("exit 1", dir); err == nil {
		t.Fatal("exit 1: want error, got nil")
	}
}

// TestShellOutputReturnsOutputAndHonorsItsLimit: ShellOutput hands back what
// the command printed, fails a command that exits non-zero, and fails one
// that outlives its limit instead of waiting for it.
func TestShellOutputReturnsOutputAndHonorsItsLimit(t *testing.T) {
	dir := t.TempDir()
	out, err := ShellOutput("echo hello", dir, time.Minute)
	if err != nil || trimEOL(out) != "hello" {
		t.Fatalf("echo: out=%q err=%v, want hello and no error", out, err)
	}
	if _, err := ShellOutput("exit 3", dir, time.Minute); err == nil {
		t.Fatal("exit 3: want an error")
	}
	slow := "sleep 30"
	if runtime.GOOS == "windows" {
		slow = "ping -n 31 127.0.0.1 >nul"
	}
	start := time.Now()
	_, err = ShellOutput(slow, dir, time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("a command past its limit: err=%v, want the limit named", err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("ShellOutput took %s past a 1s limit, want it to stop waiting", took)
	}
}

// markerClass builds an EnvClass whose up/check/down commands operate on a
// port-named marker file, so a wrong substitution anywhere in the chain
// shows up as a check or down failure.
func markerClass() manifest.EnvClass {
	return manifest.EnvClass{
		Up:    "echo {ticket}>up-{port}.marker",
		Check: existsCmd("up-{port}.marker"),
		Down:  removeCmd("up-{port}.marker"),
	}
}

func TestUpCheckDownPortSubstitution(t *testing.T) {
	dir := t.TempDir()
	c := markerClass()

	h, err := Up(c, "T-1", dir)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if h.Port <= 0 {
		t.Fatalf("Port = %d, want positive", h.Port)
	}

	markerPath := filepath.Join(dir, fmt.Sprintf("up-%d.marker", h.Port))
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker file for allocated port missing: %v", err)
	}
	if got := trimEOL(string(data)); got != "T-1" {
		t.Fatalf("marker content = %q, want T-1", got)
	}

	if err := h.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker file should be removed by Down, stat err = %v", err)
	}
}

func TestUpFailureReturnsUnavailableWithPolicy(t *testing.T) {
	dir := t.TempDir()
	c := manifest.EnvClass{Up: "exit 1", Check: "exit 0", Down: "exit 0", Unavailable: "defer-ci"}

	_, err := Up(c, "T-1", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	var u *Unavailable
	if !errors.As(err, &u) {
		t.Fatalf("expected *Unavailable, got %T: %v", err, err)
	}
	if u.Stage != "up" {
		t.Fatalf("Stage = %q, want up", u.Stage)
	}
	if u.Policy != "defer-ci" {
		t.Fatalf("Policy = %q, want defer-ci", u.Policy)
	}
}

func TestCheckFailureReturnsUnavailableWithPolicy(t *testing.T) {
	dir := t.TempDir()
	c := manifest.EnvClass{Up: "exit 0", Check: "exit 1", Down: "exit 0", Unavailable: ""}

	_, err := Up(c, "T-1", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	var u *Unavailable
	if !errors.As(err, &u) {
		t.Fatalf("expected *Unavailable, got %T: %v", err, err)
	}
	if u.Stage != "check" {
		t.Fatalf("Stage = %q, want check", u.Stage)
	}
	if u.Policy != "" {
		t.Fatalf("Policy = %q, want empty (pause)", u.Policy)
	}
}

// TestAllocatePortLoopbackOnly guards the port probe against binding every
// interface, which opens a port to the network and makes Windows Firewall
// prompt for each new jig binary.
func TestAllocatePortLoopbackOnly(t *testing.T) {
	host, _, err := net.SplitHostPort(portProbeAddr)
	if err != nil {
		t.Fatalf("split %q: %v", portProbeAddr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("portProbeAddr = %q, want a loopback address", portProbeAddr)
	}
	port, err := allocatePort()
	if err != nil {
		t.Fatalf("allocatePort: %v", err)
	}
	l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d from allocatePort is not free on %s: %v", port, host, err)
	}
	l.Close()
}

// TestShellOutputEnvGivesTheChildItsVariables: a variable passed to
// ShellOutputEnv reaches the command beside the inherited environment, and
// without one (nil, empty, or ShellOutput) it is unset.
func TestShellOutputEnvGivesTheChildItsVariables(t *testing.T) {
	dir := t.TempDir()
	// A variable the process has and the child must keep.
	inherited := "PATH"
	echo := `echo "$JIG_T $PATH"`
	if runtime.GOOS == "windows" {
		inherited = "SystemRoot"
		echo = "echo %JIG_T% %SystemRoot%"
	}
	want := os.Getenv(inherited)
	if want == "" {
		t.Skipf("%s is not set", inherited)
	}
	out, err := ShellOutputEnv(echo, dir, time.Minute, []string{"JIG_T=hello-env"})
	if err != nil || !strings.Contains(out, "hello-env") || !strings.Contains(out, want) {
		t.Fatalf("with the variable: out=%q err=%v, want hello-env and the inherited %s", out, err, inherited)
	}
	for name, run := range map[string]func() (string, error){
		"nil env":     func() (string, error) { return ShellOutputEnv(echo, dir, time.Minute, nil) },
		"empty env":   func() (string, error) { return ShellOutputEnv(echo, dir, time.Minute, []string{}) },
		"ShellOutput": func() (string, error) { return ShellOutput(echo, dir, time.Minute) },
	} {
		out, err := run()
		if err != nil || strings.Contains(out, "hello-env") || !strings.Contains(out, want) {
			t.Errorf("%s: out=%q err=%v, want the variable unset and the inherited %s kept", name, out, err, inherited)
		}
	}
}

// TestEnvironWithout drops the named variables by the rules of the OS the
// environment is for, and leaves the entries it keeps (including Windows'
// own "=C:" ones) as they are.
func TestEnvironWithout(t *testing.T) {
	environ := []string{"A=1", "JIG_RECORD_DIR=outer", "jig_record_dir=lower", "=C:=C:/work", "B=JIG_RECORD_DIR=x", "JIG_RECORD_DIRX=y"}
	if got, want := environWithout(environ, "linux", RecordDirEnv), []string{"A=1", "jig_record_dir=lower", "=C:=C:/work", "B=JIG_RECORD_DIR=x", "JIG_RECORD_DIRX=y"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("linux: %q, want %q", got, want)
	}
	if got, want := environWithout(environ, "windows", RecordDirEnv), []string{"A=1", "=C:=C:/work", "B=JIG_RECORD_DIR=x", "JIG_RECORD_DIRX=y"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("windows: %q, want %q", got, want)
	}
	if len(environ) != 6 || environ[1] != "JIG_RECORD_DIR=outer" {
		t.Errorf("the environment given was modified: %q", environ)
	}
}

// envrunHelperEnv marks a run of this test binary as the child
// TestShellOutputDoesNotPassOnTheRecordDirItInherited drives.
const envrunHelperEnv = "JIG_ENVRUN_TEST_HELPER"

// TestShellOutputHelper runs in the child of the test below, in a process
// whose environment holds a JIG_RECORD_DIR, and prints what a command it
// shells out sees. Outside that child it does nothing.
func TestShellOutputHelper(t *testing.T) {
	if os.Getenv(envrunHelperEnv) == "" {
		t.Skip("only the child of TestShellOutputDoesNotPassOnTheRecordDirItInherited runs this")
	}
	show := `if [ -n "$JIG_RECORD_DIR" ]; then echo "set-$JIG_RECORD_DIR"; else echo unset; fi`
	if runtime.GOOS == "windows" {
		show = "if defined JIG_RECORD_DIR (echo set-%JIG_RECORD_DIR%) else (echo unset)"
	}
	dir := t.TempDir()
	plain, err := ShellOutput(show, dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	given, err := ShellOutputEnv(show, dir, time.Minute, []string{RecordDirEnv + "=given"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plain=%s", strings.TrimSpace(plain))
	t.Logf("given=%s", strings.TrimSpace(given))
	// An env class's up, check and down commands (Shell) must not see it either.
	notSet := `test -z "$JIG_RECORD_DIR"`
	if runtime.GOOS == "windows" {
		notSet = "if defined JIG_RECORD_DIR exit 1"
	}
	if err := Shell(notSet, dir); err != nil {
		t.Logf("shell=saw it: %v", err)
	} else {
		t.Logf("shell=unset")
	}
}

// TestShellOutputDoesNotPassOnTheRecordDirItInherited: jig can run inside a
// recording oracle run, so its own environment may hold JIG_RECORD_DIR. A
// command ShellOutput runs (the gate's oracle) must not see it, and a command
// ShellOutputEnv gives one must see that one and no other. The environment is
// set on a child process, never on this one.
func TestShellOutputDoesNotPassOnTheRecordDirItInherited(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestShellOutputHelper$", "-test.v")
	cmd.Env = append(os.Environ(), envrunHelperEnv+"=1", RecordDirEnv+"=outer")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "set-outer") {
		t.Errorf("a command saw the inherited JIG_RECORD_DIR:\n%s", got)
	}
	for _, want := range []string{"plain=unset", "given=set-given", "shell=unset"} {
		if !strings.Contains(got, want) {
			t.Errorf("helper output lacks %q:\n%s", want, got)
		}
	}
}

// envrunPWDHelperEnv marks a run of this test binary as the command
// TestShellOutputGivesTheChildThePWDOfItsDir shells out.
const envrunPWDHelperEnv = "JIG_ENVRUN_TEST_PWD_HELPER"

// TestPWDHelper runs as the command TestShellOutputGivesTheChildThePWDOfItsDir
// shells out, and prints the PWD it was given. Outside that it does nothing.
func TestPWDHelper(t *testing.T) {
	if os.Getenv(envrunPWDHelperEnv) == "" {
		t.Skip("only the command of TestShellOutputGivesTheChildThePWDOfItsDir runs this")
	}
	t.Logf("pwd=%s", os.Getenv("PWD"))
}

// TestShellOutputGivesTheChildThePWDOfItsDir: a command's environment is the
// inherited one with the PWD os/exec sets for the directory it runs in. Setting
// Env by hand drops that PWD, and a child exec'd without a shell resetting it
// (as a session's claude is, which internal/session's test pins) gets jig's
// own, which names another directory. The subtests through sh only show that
// a command runs with the right PWD, since sh discards a PWD that is not its
// cwd; ChildEnv's own assertion is what pins the function. Windows has no PWD.
func TestShellOutputGivesTheChildThePWDOfItsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no PWD")
	}
	dir := t.TempDir()
	if wd, err := os.Getwd(); err != nil || filepath.Clean(wd) == filepath.Clean(dir) {
		t.Fatalf("the test needs to run elsewhere than %s (cwd %q, %v)", dir, wd, err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := "'" + exe + "' -test.run='^TestPWDHelper$' -test.v"
	for _, tc := range []struct {
		name string
		run  func() (string, error)
	}{
		{"ShellOutput", func() (string, error) { return ShellOutput(envrunPWDHelperEnv+"=1 "+child, dir, time.Minute) }},
		{"ShellOutputEnv", func() (string, error) {
			return ShellOutputEnv(child, dir, time.Minute, []string{envrunPWDHelperEnv + "=1"})
		}},
	} {
		out, err := tc.run()
		if err != nil || !strings.Contains(out, "pwd="+dir+"\n") {
			t.Errorf("%s: out=%q err=%v, want the child's PWD to be %s", tc.name, out, err, dir)
		}
	}

	// And as a plain function of the command.
	c := exec.Command("true")
	c.Dir = dir
	var pwd string
	for _, kv := range ChildEnv(c) {
		if v, ok := strings.CutPrefix(kv, "PWD="); ok {
			pwd = v
		}
	}
	if pwd != dir {
		t.Errorf("ChildEnv PWD = %q, want %q", pwd, dir)
	}
}
