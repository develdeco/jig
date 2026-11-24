package main

import (
	"errors"
	"strings"
	"testing"
)

// TestTUIOpenerNeedsATerminalAndHTTP: the screens are given an opener only on a
// terminal, and it sends only an http or https address to the system's opener
// of the platform, refusing any other before anything runs.
func TestTUIOpenerNeedsATerminalAndHTTP(t *testing.T) {
	t.Parallel()
	if tuiOpener(false) != nil {
		t.Errorf("a pipe was given an opener")
	}
	if tuiOpener(true) == nil {
		t.Errorf("a terminal was given none")
	}

	const page = "https://example.test/acme/api/pull/12"
	for goos, want := range map[string]string{
		"windows": "rundll32 url.dll,FileProtocolHandler " + page,
		"darwin":  "open " + page,
		"linux":   "xdg-open " + page,
		"freebsd": "xdg-open " + page,
	} {
		var ran []string
		err := openURL(goos, page, func(name string, args ...string) error {
			ran = append(ran, name+" "+strings.Join(args, " "))
			return nil
		})
		if err != nil || len(ran) != 1 || ran[0] != want {
			t.Errorf("%s: ran %q (%v), want %q", goos, ran, err, want)
		}
	}
	if err := openURL("linux", "http://example.test/x", func(string, ...string) error { return nil }); err != nil {
		t.Errorf("an http address was refused: %v", err)
	}

	for _, bad := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "ftp://example.test/x", "ms-msdt:/id", "calc.exe", "//example.test/x", "https://", "",
		"https://exa mple.test/%zz",
	} {
		ran := false
		err := openURL("windows", bad, func(string, ...string) error { ran = true; return nil })
		if err == nil || ran {
			t.Errorf("%q: opened (%v), want a refusal before anything runs", bad, err)
		}
	}

	// The opener's own failure comes back as it is.
	boom := errors.New("no opener on this machine")
	if err := openURL("linux", page, func(string, ...string) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("a failing opener returned %v", err)
	}
}
