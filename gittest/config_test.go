package gittest_test

import (
	"os"
	"testing"

	"github.com/develdeco/jig/gittest"
	"github.com/develdeco/jig/gitx"
)

func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}

// TestRunSetsHermeticConfig proves Run's own effect: a repo created inside
// this test binary sees maintenance.auto, receive.autogc and gc.autoDetach
// as false, with no other config source (like a host's system config)
// overriding them.
func TestRunSetsHermeticConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	for _, key := range []string{"maintenance.auto", "receive.autogc", "gc.autoDetach"} {
		got, err := gitx.Run(dir, "config", "--get", key)
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got != "false" {
			t.Errorf("%s = %q, want %q", key, got, "false")
		}
	}
}
