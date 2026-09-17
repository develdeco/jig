package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestCmdVersion checks the `jig version` output shape: the AXI version
// block carries the same version and commit strings formatVersion and
// formatCommit would derive from this test binary's own build info, and
// the block is followed by a help hint.
func TestCmdVersion(t *testing.T) {
	var buf bytes.Buffer
	code := cmdVersion(nil, &buf)
	if code != 0 {
		t.Fatalf("cmdVersion exit code = %d, output:\n%s", code, buf.String())
	}

	info, _ := debug.ReadBuildInfo()
	wantVersion := formatVersion(info)
	wantCommit := formatCommit(info)

	out := buf.String()
	if !strings.Contains(out, "version:\n") {
		t.Errorf("expected a \"version:\" block header, got:\n%s", out)
	}
	if !strings.Contains(out, "  version: "+axi.Quote(wantVersion)) {
		t.Errorf("expected \"version: %s\", got:\n%s", wantVersion, out)
	}
	if !strings.Contains(out, "  commit: "+axi.Quote(wantCommit)) {
		t.Errorf("expected \"commit: %s\", got:\n%s", wantCommit, out)
	}
	if !strings.Contains(out, "go: ") {
		t.Errorf("expected a go line, got:\n%s", out)
	}
	if !strings.Contains(out, "help[1]:") {
		t.Errorf("expected a help hint, got:\n%s", out)
	}
}

// TestMainVersionCommand drives `jig version` through Main end to end.
func TestMainVersionCommand(t *testing.T) {
	var buf bytes.Buffer
	code := Main([]string{"version"}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig version exit code = %d, output:\n%s", code, buf.String())
	}

	info, _ := debug.ReadBuildInfo()
	wantVersion := formatVersion(info)
	if !strings.Contains(buf.String(), "  version: "+axi.Quote(wantVersion)) {
		t.Fatalf("expected \"version: %s\", got:\n%s", wantVersion, buf.String())
	}
}

// TestFormatVersion covers formatVersion's derivation from build info: the
// exact tag, a pseudo-version, a pseudo-version (or tag) with the "+dirty"
// suffix Go's own toolchain already appends, and the (devel) fallbacks.
func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "tag",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}},
			want: "v9.9.9",
		},
		{
			name: "pseudo-version",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260101120000-abcdef012345"}},
			want: "v0.0.0-20260101120000-abcdef012345",
		},
		{
			name: "pseudo-version dirty",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260101120000-abcdef012345+dirty"}},
			want: "v0.0.0-20260101120000-abcdef012345+dirty",
		},
		{
			name: "tag dirty",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9+dirty"}},
			want: "v9.9.9+dirty",
		},
		{
			name: "nil build info",
			info: nil,
			want: "(devel)",
		},
		{
			name: "empty main version",
			info: &debug.BuildInfo{Main: debug.Module{Version: ""}},
			want: "(devel)",
		},
		{
			name: "explicit devel (e.g. -buildvcs=false)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: "(devel)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatVersion(tt.info); got != tt.want {
				t.Errorf("formatVersion(%+v) = %q, want %q", tt.info, got, tt.want)
			}
		})
	}
}

// TestFormatCommit covers formatCommit's derivation from build info: a
// short revision, a long revision truncated to 12 characters, and the
// "unknown" fallbacks when vcs settings are missing.
func TestFormatCommit(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "short revision",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123"},
			}},
			want: "abc123",
		},
		{
			name: "long revision truncated to 12 chars",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef0123456789fedcba"},
			}},
			want: "abcdef012345",
		},
		{
			name: "missing vcs settings",
			info: &debug.BuildInfo{Settings: nil},
			want: "unknown",
		},
		{
			name: "vcs.revision present but empty",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: ""},
			}},
			want: "unknown",
		},
		{
			name: "nil build info",
			info: nil,
			want: "unknown",
		},
		{
			name: "dirty tree: no separate +dirty suffix on commit",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123def456"},
				{Key: "vcs.modified", Value: "true"},
			}},
			want: "abc123def456",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCommit(tt.info); got != tt.want {
				t.Errorf("formatCommit(%+v) = %q, want %q", tt.info, got, tt.want)
			}
		})
	}
}

// TestDirtySuffixAppearsOnce guards the specific failure mode this package
// used to have: Go's toolchain already appends "+dirty" to Main.Version for
// a modified tree, so formatVersion must pass that through untouched, and
// formatCommit must not append its own "+dirty" from vcs.modified. Across
// both fields combined, "+dirty" must appear exactly once.
func TestDirtySuffixAppearsOnce(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.0.0-20260101120000-abcdef012345+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef012345678"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	combined := formatVersion(info) + " " + formatCommit(info)
	if n := strings.Count(combined, "+dirty"); n != 1 {
		t.Fatalf("expected \"+dirty\" to appear exactly once, appeared %d times in %q", n, combined)
	}
}

// TestFormatVersionAndCommitSmoke is a smoke check that both functions
// never panic and always return a non-empty string against this test
// binary's own real build info, whatever it happens to be.
func TestFormatVersionAndCommitSmoke(t *testing.T) {
	info, _ := debug.ReadBuildInfo()
	if got := formatVersion(info); got == "" {
		t.Error("formatVersion() returned an empty string")
	}
	if got := formatCommit(info); got == "" {
		t.Error("formatCommit() returned an empty string")
	}
}
