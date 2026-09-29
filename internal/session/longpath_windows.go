//go:build windows

package session

import (
	"path/filepath"
	"syscall"
)

// longPath returns absolute p with every Windows 8.3 short-name component
// (RUNNER~1) spelled out in full. Windows only expands a path that exists,
// so a path whose tail does not exist yet - a result file before the
// session writes it - has its longest existing prefix expanded and the
// rest kept as written. It returns p unchanged when p is relative, since
// sessionView replaces p's text wherever the prompt holds it, or when
// expanding fails.
func longPath(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	rest := ""
	for dir := filepath.Clean(p); ; {
		if long, ok := getLongPathName(dir); ok {
			if rest == "" {
				return long
			}
			return filepath.Join(long, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// getLongPathName is GetLongPathNameW on p, which must exist.
func getLongPathName(p string) (string, bool) {
	in, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, err := syscall.GetLongPathName(in, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return "", false
		}
		// n counts the terminating NUL only when buf was too small.
		if int(n) < len(buf) {
			return syscall.UTF16ToString(buf[:n]), true
		}
		buf = make([]uint16, n)
	}
}
