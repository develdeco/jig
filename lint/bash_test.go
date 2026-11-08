package lint

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requireBash returns a bash that runs a workflow step the way the runner
// does, or skips the test saying why there is none (what names the step).
// Elsewhere it is whatever exec.LookPath finds. On Windows a bash first on
// PATH can be WSL's launcher, which runs the script in a Linux the test does
// not mean (and fails on a Windows path), so it takes Git for Windows' own:
// from the PATH entries, then the standard install directories. It never
// looks up or runs git, which this package may not (TestNoGitSpawnOutsideGitx).
func requireBash(t *testing.T, what string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skipf("no bash to run %s: %v", what, err)
		}
		return bash
	}
	var candidates []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			candidates = append(candidates, filepath.Join(dir, "bash.exe"))
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LocalAppData"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		if env == "LocalAppData" {
			root = filepath.Join(root, "Programs")
		}
		candidates = append(candidates,
			filepath.Join(root, "Git", "bin", "bash.exe"),
			filepath.Join(root, "Git", "usr", "bin", "bash.exe"))
	}
	for _, c := range candidates {
		if isWSLLauncher(c) {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	t.Skipf("no Git for Windows bash to run %s (WSL's launcher does not count)", what)
	return ""
}

// isWSLLauncher reports whether path is Windows' own bash.exe, the launcher
// of the Windows Subsystem for Linux: the one in System32 or the store stub
// under WindowsApps.
func isWSLLauncher(path string) bool {
	p := "/" + strings.ToLower(strings.ReplaceAll(path, `\`, "/")) + "/"
	return strings.Contains(p, "/windows/system32/") || strings.Contains(p, "/windows/sysnative/") || strings.Contains(p, "/windowsapps/")
}

func TestIsWSLLauncher(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		`C:\Windows\System32\bash.exe`:                            true,
		`c:\windows\system32\bash.exe`:                            true,
		`C:\Windows\Sysnative\bash.exe`:                           true,
		`C:\Users\x\AppData\Local\Microsoft\WindowsApps\bash.exe`: true,
		`C:\Program Files\Git\bin\bash.exe`:                       false,
		`C:\Program Files\Git\usr\bin\bash.exe`:                   false,
		`D:\tools\git\bin\bash.exe`:                               false,
	} {
		if got := isWSLLauncher(path); got != want {
			t.Errorf("isWSLLauncher(%s) = %v, want %v", path, got, want)
		}
	}
}
