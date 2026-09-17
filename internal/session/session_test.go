package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestAvailable checks the backend preflight against an empty PATH and a PATH
// holding stand-in programs.
func TestAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, c := range []struct{ goos, name string }{
		{"linux", "headless"}, {"linux", "herdr"}, {"windows", "herdr"},
	} {
		var ae *axi.Error
		if err := available(c.goos, c.name); !errors.As(err, &ae) || ae.Code != "BACKEND_UNAVAILABLE" {
			t.Errorf("available(%s, %s) with an empty PATH = %v, want BACKEND_UNAVAILABLE", c.goos, c.name, err)
		}
	}
	if err := available(runtime.GOOS, "fake"); err != nil {
		t.Errorf("fake backend: %v, want nil", err)
	}

	bin := t.TempDir()
	for _, prog := range []string{"claude", "herdr", "wsl"} {
		name := prog
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte{}, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for _, name := range []string{"headless", "herdr"} {
		if err := available(runtime.GOOS, name); err != nil {
			t.Errorf("available(%s) with the program on PATH: %v", name, err)
		}
	}
}
