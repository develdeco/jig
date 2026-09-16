package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestCmdVersion checks the `jig version` output shape: the AXI version
// block names the pinned release version, and the block is followed by a
// help hint.
func TestCmdVersion(t *testing.T) {
	var buf bytes.Buffer
	code := cmdVersion(nil, &buf)
	if code != 0 {
		t.Fatalf("cmdVersion exit code = %d, output:\n%s", code, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "version:\n") {
		t.Errorf("expected a \"version:\" block header, got:\n%s", out)
	}
	if !strings.Contains(out, "version: 0.1.0") {
		t.Errorf("expected \"version: 0.1.0\", got:\n%s", out)
	}
	if !strings.Contains(out, "commit: ") {
		t.Errorf("expected a commit line, got:\n%s", out)
	}
	if !strings.Contains(out, "go: ") {
		t.Errorf("expected a go line, got:\n%s", out)
	}
	if !strings.Contains(out, "help[1]:") {
		t.Errorf("expected a help hint, got:\n%s", out)
	}
}

// TestBuildCommitUnknownWithoutVCSStamp is a smoke check that buildCommit
// never panics and always returns a non-empty string, whether or not this
// test binary happens to carry a vcs.revision build setting.
func TestBuildCommitUnknownWithoutVCSStamp(t *testing.T) {
	if got := buildCommit(); got == "" {
		t.Fatal("buildCommit() returned an empty string")
	}
}

// TestMainVersionCommand drives `jig version` through Main end to end.
func TestMainVersionCommand(t *testing.T) {
	var buf bytes.Buffer
	code := Main([]string{"version"}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig version exit code = %d, output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "version: 0.1.0") {
		t.Fatalf("expected \"version: 0.1.0\", got:\n%s", buf.String())
	}
}
