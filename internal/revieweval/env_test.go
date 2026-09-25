package revieweval

import (
	"reflect"
	"sort"
	"testing"
)

// TestDispatchEnvDropsOwnedAndScaffoldingVariables pins dispatchEnv's own
// denylist: a JIG_-prefixed name, the two gittest.Run scaffolding
// variables, and PWD/OLDPWD are all dropped; everything else passes
// through unchanged.
func TestDispatchEnvDropsOwnedAndScaffoldingVariables(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/dev",
		"JIG_REVIEWEVAL_BACKEND=headless",
		"JIG_HEADLESS_TIMEOUT=45m",
		"GIT_CONFIG_GLOBAL=/tmp/jig-gittest123/gitconfig",
		"GIT_CONFIG_NOSYSTEM=1",
		"PWD=/repo/internal/revieweval",
		"OLDPWD=/repo",
		"FOO=bar",
	}
	got := dispatchEnv("linux", in)
	want := []string{"PATH=/usr/bin", "HOME=/home/dev", "FOO=bar"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dispatchEnv(linux, ...) = %v, want %v", got, want)
	}
}

// TestDispatchEnvDropsLaunchContextEntries pins the generic launch-context
// rule: an empty name, a name starting "=" (Windows' own hidden per-drive
// current-directory entries, which cmd.exe sets and every process it
// starts inherits - strings.Cut reads their own name as "" since they
// hold a second "=" of their own), "_" (a shell's last-argument variable,
// or a directly-run test binary's own path) and GOCOVERDIR (present only
// under "go test -cover") are all dropped, whatever process started this
// one - none of them is anything a dispatch owns or a session needs, only
// launch scaffolding.
func TestDispatchEnvDropsLaunchContextEntries(t *testing.T) {
	in := []string{
		`=C:=C:\src\jig\internal\revieweval`,
		`=D:=D:\src\jig`,
		`=ExitCode=00000000`,
		"_=/usr/local/go/bin/go",
		"GOCOVERDIR=/tmp/go-build123/b001/gocoverdir",
		"PATH=/usr/bin",
		"FOO=bar",
	}
	got := dispatchEnv("linux", in)
	want := []string{"PATH=/usr/bin", "FOO=bar"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dispatchEnv(linux, ...) = %v, want %v", got, want)
	}
}

// TestDispatchEnvDropsGOCOVERDIRCaseInsensitivelyOnWindows extends the
// windows-only case-insensitive comparison (already pinned for JIG_ and
// GIT_CONFIG_* below) to GOCOVERDIR.
func TestDispatchEnvDropsGOCOVERDIRCaseInsensitivelyOnWindows(t *testing.T) {
	in := []string{"GoCoverDir=C:\\cover", "FOO=bar"}
	got := dispatchEnv("windows", in)
	want := []string{"FOO=bar"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dispatchEnv(windows, ...) = %v, want %v: GoCoverDir must be dropped case-insensitively", got, want)
	}

	gotLinux := dispatchEnv("linux", in)
	wantLinux := []string{"GoCoverDir=C:\\cover", "FOO=bar"}
	sort.Strings(gotLinux)
	sort.Strings(wantLinux)
	if !reflect.DeepEqual(gotLinux, wantLinux) {
		t.Errorf("dispatchEnv(linux, ...) = %v, want everything kept (only exact-case GOCOVERDIR matches off windows)", gotLinux)
	}
}

// TestDispatchEnvComparesNamesCaseInsensitivelyOnWindows pins the goos
// distinction: on windows, a differently-cased name still counts as owned
// or scaffolding and is dropped; off windows, only an exact-case match is.
func TestDispatchEnvComparesNamesCaseInsensitivelyOnWindows(t *testing.T) {
	in := []string{
		"Path=C:\\Windows",
		"Jig_Headless_Timeout=45m",
		"git_config_global=C:\\tmp\\gitconfig",
		"Pwd=C:\\repo",
		"FOO=bar",
	}

	got := dispatchEnv("windows", in)
	want := []string{"Path=C:\\Windows", "FOO=bar"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dispatchEnv(windows, ...) = %v, want %v", got, want)
	}

	gotLinux := dispatchEnv("linux", in)
	wantLinux := []string{"Path=C:\\Windows", "Jig_Headless_Timeout=45m", "git_config_global=C:\\tmp\\gitconfig", "Pwd=C:\\repo", "FOO=bar"}
	sort.Strings(gotLinux)
	sort.Strings(wantLinux)
	if !reflect.DeepEqual(gotLinux, wantLinux) {
		t.Errorf("dispatchEnv(linux, ...) with mixed-case names = %v, want everything kept (only exact-case names match off windows)", gotLinux)
	}
}
