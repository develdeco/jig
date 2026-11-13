package gitx

import (
	"path/filepath"
	"testing"
)

// TestHooksConfigured: core.hooksPath in the repository's config, the global
// config the environment names, or the environment's own config variables is
// a hook; an include or an unreadable file is taken for one; a plain config
// is not.
func TestHooksConfigured(t *testing.T) {
	dir := newStoreRepo(t)
	cfg := func(body string) []string {
		p := filepath.Join(t.TempDir(), "gitconfig")
		writeFileT(t, filepath.Dir(p), filepath.Base(p), body)
		return []string{"GIT_CONFIG_GLOBAL=" + p}
	}
	if HooksConfigured(dir, cfg("[user]\n\tname = x\n")) {
		t.Fatal("a global config without hooksPath reads as hooked")
	}
	if !HooksConfigured(dir, cfg("[core]\n\thooksPath = /h\n")) {
		t.Fatal("a global core.hooksPath reads as not hooked")
	}
	if !HooksConfigured(dir, cfg("[include]\n\tpath = other\n")) {
		t.Fatal("a global include reads as not hooked")
	}
	if !HooksConfigured(dir, []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/h"}) {
		t.Fatal("an environment core.hooksPath reads as not hooked")
	}
	mustRun(t, dir, "config", "core.hooksPath", "/h")
	if !HooksConfigured(dir, cfg("")) {
		t.Fatal("a repository core.hooksPath reads as not hooked")
	}
}
