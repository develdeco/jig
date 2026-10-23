package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileKeepsModeOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to keep")
	}
	dir := t.TempDir()
	private := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(private, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(private, []byte("new"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(private); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	if got := readOrFail(t, private); got != "new" {
		t.Errorf("content = %q", got)
	}

	if err := writeFile(private, []byte("again"), 0o755, false); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(private); info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 set", info.Mode().Perm())
	}

	fresh := filepath.Join(dir, "a", "b", "c.json")
	if err := writeFile(fresh, []byte("x"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(fresh); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("new file = %v, %v, want 0644", info, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(fresh))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}
