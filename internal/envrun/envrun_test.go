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
	start := time.Now()
	_, err = ShellOutput("sleep 30", dir, time.Second)
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
		Up:    "echo {ticket} >up-{port}.marker",
		Check: "test -f up-{port}.marker",
		Down:  "rm -f up-{port}.marker",
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
	// A variable the process has and the child must keep: PATH, which Git's
	// sh rewrites on Windows, so there SystemRoot, which it names SYSTEMROOT.
	inherited, want := "PATH", os.Getenv("PATH")
	if runtime.GOOS == "windows" {
		inherited, want = "SYSTEMROOT", os.Getenv("SystemRoot")
	}
	echo := `echo "$JIG_T $` + inherited + `"`
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
	if err := Shell(`test -z "$JIG_RECORD_DIR"`, dir); err != nil {
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

// TestACommandReachesShAsWritten: a command is sh's to read on every OS, so
// a | in single quotes is no pipe and two backslashes in single quotes stay
// two, and the variable that carried the command to sh does not reach it. It
// goes the same through Shell, which an env class's commands take.
func TestACommandReachesShAsWritten(t *testing.T) {
	dir := t.TempDir()
	cmd := `printf '[%s]\n' 'A|B' 'a\\b' "c d" "${` + commandEnv + `-unset}"`
	out, err := ShellOutput(cmd, dir, time.Minute)
	if want := "[A|B]\n[a\\\\b]\n[c d]\n[unset]\n"; err != nil || out != want {
		t.Fatalf("ShellOutput(%q) = %q, %v, want %q", cmd, out, err, want)
	}
	if err := Shell(`test "$(printf '%s' 'A|B')" = 'A|B'`, dir); err != nil {
		t.Errorf("Shell of a single-quoted |: %v", err)
	}
}

// TestFindSh: off Windows sh is the one on PATH. On Windows it is Git for
// Windows' bin\sh.exe, in the nearest of the PATH git's directory and the
// three above it, then the sh on PATH; with neither the error says what to
// install.
func TestFindSh(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "Git")
	gitSh := filepath.Join(install, "bin", "sh.exe")
	for _, p := range []string{gitSh, filepath.Join(root, "other", "sh.exe")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pathSh := filepath.Join(root, "other", "sh.exe")
	notFound := errors.New("not found")
	for _, tc := range []struct {
		name      string
		goos      string
		git, sh   string // what PATH holds, "" for nothing
		want, err string
	}{
		{"sh on PATH", "linux", "", "/bin/sh", "/bin/sh", ""},
		{"no sh on PATH", "darwin", "/usr/bin/git", "", "", "found no sh on PATH"},
		{"git in cmd", "windows", filepath.Join(install, "cmd", "git.exe"), pathSh, gitSh, ""},
		{"git in bin", "windows", filepath.Join(install, "bin", "git.exe"), "", gitSh, ""},
		{"git in mingw64/bin", "windows", filepath.Join(install, "mingw64", "bin", "git.exe"), "", gitSh, ""},
		{"git in mingw64/libexec/git-core", "windows", filepath.Join(install, "mingw64", "libexec", "git-core", "git.exe"), "", gitSh, ""},
		{"git deeper than the install's", "windows", filepath.Join(install, "a", "b", "c", "d", "git.exe"), pathSh, pathSh, ""},
		{"git with no sh beside it", "windows", filepath.Join(root, "other", "git", "cmd", "git.exe"), pathSh, pathSh, ""},
		{"no git", "windows", "", pathSh, pathSh, ""},
		{"neither", "windows", filepath.Join(root, "nowhere", "cmd", "git.exe"), "", "", "install Git for Windows, or put an sh on PATH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			look := func(name string) (string, error) {
				if p := map[string]string{"git": tc.git, "sh": tc.sh}[name]; p != "" {
					return p, nil
				}
				return "", notFound
			}
			got, err := findSh(tc.goos, look, isFile)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("findSh = %q, %v, want an error that says %q", got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("findSh = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// envrunShHelperEnv marks a run of this test binary as the child of one of
// the tests that run TestShHelper.
const envrunShHelperEnv = "JIG_ENVRUN_TEST_SH_HELPER"

// TestShHelper runs in the children runShHelper starts and logs what running
// `echo own` through this package gave: ShellOutput, ShellOutputEnv handed a
// JIG_SHELL_COMMAND of its own among its variables, and Shell. Outside them
// it does nothing.
func TestShHelper(t *testing.T) {
	if os.Getenv(envrunShHelperEnv) == "" {
		t.Skip("only the children runShHelper starts run this")
	}
	dir := t.TempDir()
	out, err := ShellOutput("echo own", dir, time.Minute)
	t.Logf("output=%q err=%v", strings.TrimSpace(out), err)
	out, err = ShellOutputEnv("echo own", dir, time.Minute, []string{commandEnv + "=echo given"})
	t.Logf("outputenv=%q err=%v", strings.TrimSpace(out), err)
	t.Logf("shell err=%v", Shell("echo own", dir))
}

// runShHelper runs TestShHelper in a child of this test binary whose
// environment is this process's with the variables in set put in place of
// any of the same name, and returns what the child printed. The variables are
// set on the child, never on this process.
func runShHelper(t *testing.T, set map[string]string) string {
	t.Helper()
	names := []string{envrunShHelperEnv}
	for name := range set {
		names = append(names, name)
	}
	env := append(environWithout(os.Environ(), runtime.GOOS, names...), envrunShHelperEnv+"=1")
	for name, value := range set {
		env = append(env, name+"="+value)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestShHelper$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	return string(out)
}

// TestWithNoShEveryCommandFails: on a machine with no sh, ShellOutput,
// ShellOutputEnv and Shell each fail and say why, and none passes a command
// it did not run: the gate's oracle runs and an env class's up and check
// rest on it. The child's PATH is an empty directory, so it finds neither
// git nor sh.
func TestWithNoShEveryCommandFails(t *testing.T) {
	out := runShHelper(t, map[string]string{"PATH": t.TempDir()})
	why := "found no sh on PATH"
	if runtime.GOOS == "windows" {
		why = "install Git for Windows, or put an sh on PATH"
	}
	for _, run := range []string{`output="" err=`, `outputenv="" err=`, "shell err="} {
		if !strings.Contains(out, run+`shell "echo own": jig runs oracles and env class commands with sh`) {
			t.Errorf("the child's %s... line does not fail for want of sh:\n%s", run, out)
		}
	}
	if n := strings.Count(out, why); n != 3 {
		t.Errorf("the child said %q %d times, want once for each of the 3 runs:\n%s", why, n, out)
	}
}

// TestTheVariableThatCarriesACommandNeverReplacesIt: sh runs the command jig
// was handed, never a JIG_SHELL_COMMAND the process inherited or a caller
// passed among a command's variables.
func TestTheVariableThatCarriesACommandNeverReplacesIt(t *testing.T) {
	out := runShHelper(t, map[string]string{commandEnv: "echo inherited"})
	for _, want := range []string{`output="own" err=<nil>`, `outputenv="own" err=<nil>`, "shell err=<nil>"} {
		if !strings.Contains(out, want) {
			t.Errorf("the child's output lacks %q:\n%s", want, out)
		}
	}
}
