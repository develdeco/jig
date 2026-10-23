package observe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendExportsWritesOneQuotedLinePerVariable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "env.sh")
	// A file another hook left without a final newline gets one first.
	if err := os.WriteFile(path, []byte("export A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := appendExports(path, map[string]string{"JIG_SESSION": "s-1", "Q": "it's $HOME"})
	if !set || err != nil {
		t.Fatalf("appendExports = %v, %v, want written", set, err)
	}
	want := "export A=1\nexport JIG_SESSION='s-1'\nexport Q='it'\\''s $HOME'\n"
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if set, err := appendExports(path, map[string]string{"NOT A NAME": "x"}); set || err == nil {
		t.Errorf("appendExports of a bad name = %v, %v, want a refusal", set, err)
	}
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("a refused call wrote to the file: %q", got)
	}
}

func TestClaudeSetsTheEnvironmentOnlyThroughItsEnvFile(t *testing.T) {
	t.Parallel()
	a, _ := AdapterFor("claude")
	s, ok := a.(EnvSetter)
	if !ok {
		t.Fatal("the claude adapter is no EnvSetter")
	}
	vars := map[string]string{"JIG_SESSION": "s1"}
	if set, err := s.SetEnv(func(string) string { return "" }, vars); set || err != nil {
		t.Errorf("SetEnv with no CLAUDE_ENV_FILE = %v, %v, want nothing set", set, err)
	}
	path := filepath.Join(t.TempDir(), "sessionstart-hook-0.sh")
	getenv := func(k string) string {
		if k == "CLAUDE_ENV_FILE" {
			return path
		}
		return ""
	}
	if set, err := s.SetEnv(getenv, vars); !set || err != nil {
		t.Fatalf("SetEnv = %v, %v, want set", set, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "export JIG_SESSION='s1'\n" {
		t.Errorf("file = %q", got)
	}
}
