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

func TestClaudeSetsTheEnvironmentOnlyThroughTheSessionsEnvFile(t *testing.T) {
	t.Parallel()
	claude, _ := AdapterFor("claude")
	s, ok := claude.(EnvSetter)
	if !ok {
		t.Fatal("the claude adapter is no EnvSetter")
	}
	ev := Event{Kind: KindSessionStart, Session: "s1"}
	vars := map[string]string{"JIG_SESSION": "s1"}
	envAt := func(path string) func(string) string {
		return func(k string) string {
			if k == "CLAUDE_ENV_FILE" {
				return path
			}
			return ""
		}
	}
	if set, err := s.SetEnv(ev, envAt(""), vars); set || err != nil {
		t.Errorf("SetEnv with no CLAUDE_ENV_FILE = %v, %v, want nothing set", set, err)
	}
	dir := filepath.Join(t.TempDir(), "s1")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sessionstart-hook-0.sh")
	if set, err := s.SetEnv(Event{Kind: KindSessionStart}, envAt(path), vars); set || err != nil {
		t.Errorf("SetEnv for an event with no session = %v, %v, want nothing set", set, err)
	}
	if set, err := s.SetEnv(Event{Kind: KindSessionStart, Session: "s2"}, envAt(path), vars); set || err != nil {
		t.Errorf("SetEnv into another session's directory = %v, %v, want nothing set", set, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused SetEnv made the file (stat: %v)", err)
	}
	if set, err := s.SetEnv(ev, envAt(path), vars); !set || err != nil {
		t.Fatalf("SetEnv = %v, %v, want set", set, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "export JIG_SESSION='s1'\n" {
		t.Errorf("file = %q", got)
	}
	codex, _ := AdapterFor("codex")
	if set, err := codex.(EnvSetter).SetEnv(ev, envAt(path), vars); set || err != nil {
		t.Errorf("codex SetEnv = %v, %v, want nothing set: Codex gives hooks no env file", set, err)
	}
}
