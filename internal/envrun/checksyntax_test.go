package envrun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSyntax: CheckSyntax asks the sh jig runs oracles with whether it can
// parse a command, feeding the bytes on standard input as written; a command
// sh rejects is a *SyntaxError carrying sh's message.
func TestCheckSyntax(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		ok   bool
		msg  string
	}{
		{"compound", `go test -count=1 ./lint -run 'TestX|TestY' && ! grep -rn "a\\b" . | wc -l # why`, true, ""},
		{"quotes and bang", `test "$(echo 'a  b')" = "a  b" && ! false`, true, ""},
		{"prose in parentheses", `go test -count=1 ./lint -run TestX (extended to pin the order sentence: a, b) && go vet ./lint`, false, "("},
		{"prose after a package", `go test ./internal/brief -run TestY (extended: with the new case) && go vet ./...`, false, "("},
		{"open quote", `echo "unterminated`, false, ""},
		{"open if", "if true; then\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSyntax(tc.cmd)
			if tc.ok {
				if err != nil {
					t.Fatalf("CheckSyntax(%q) = %v, want nil", tc.cmd, err)
				}
				return
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("CheckSyntax(%q) = %v, want a *SyntaxError", tc.cmd, err)
			}
			if se.Msg == "" || !strings.Contains(se.Msg, tc.msg) {
				t.Errorf("SyntaxError.Msg = %q, want sh's message containing %q", se.Msg, tc.msg)
			}
		})
	}
	// The command is never run: a parseable one that would leave a file does not.
	marker := filepath.Join(t.TempDir(), "ran")
	if err := CheckSyntax("echo x > '" + filepath.ToSlash(marker) + "'"); err != nil {
		t.Fatalf("CheckSyntax of a redirect: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("CheckSyntax ran the command")
	}
}

// TestCheckSyntaxWithNoShIsNotASyntaxError: with no sh to ask, the answer is
// an error that is not a *SyntaxError, so a caller stores what it was given.
func TestCheckSyntaxWithNoShIsNotASyntaxError(t *testing.T) {
	t.Setenv("PATH", "")
	err := CheckSyntax("echo (")
	if err == nil {
		t.Fatal("CheckSyntax with no sh on PATH = nil, want an error")
	}
	var se *SyntaxError
	if errors.As(err, &se) {
		t.Fatalf("CheckSyntax with no sh = %v, want an error that is not a *SyntaxError", err)
	}
}
