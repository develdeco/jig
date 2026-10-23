package observe

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// envName is what a variable handed to a shell may be named.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// appendExports appends to the shell file at path one `export NAME='value'`
// line per variable, sorted by name, first ending a line the file left open.
// It reports whether the lines were written.
func appendExports(path string, vars map[string]string) (bool, error) {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if !envName.MatchString(name) {
			return false, fmt.Errorf("observe: %q cannot be the name of a shell variable", name)
		}
		fmt.Fprintf(&b, "export %s=%s\n", name, shellQuote(vars[name]))
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("observe: the session's environment file: %w", err)
	}
	text := b.String()
	if endsMidLine(f) {
		text = "\n" + text
	}
	_, werr := f.WriteString(text)
	if err := errors.Join(werr, f.Close()); err != nil {
		return false, fmt.Errorf("observe: the session's environment file: %w", err)
	}
	return true, nil
}

// endsMidLine reports whether f holds text whose last byte is not a newline.
func endsMidLine(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// shellQuote quotes s for a POSIX shell: in single quotes, each single quote
// of s closed, escaped and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
