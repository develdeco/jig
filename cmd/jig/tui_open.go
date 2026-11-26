package main

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// tuiOpener is the tui's Config.Open: a function that sends an address to the
// system's opener, only when the screens own a terminal. A pipe or a script has
// no one to show a page to, so the key says the address instead.
func tuiOpener(terminal bool) func(string) error {
	if !terminal {
		return nil
	}
	return func(raw string) error {
		return openURL(runtime.GOOS, raw, func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			if err := cmd.Start(); err != nil {
				return err
			}
			go func() { _ = cmd.Wait() }()
			return nil
		})
	}
}

// openURL sends raw to the opener of goos through start, and only when it is an
// http or https address: any other scheme (a file, a script, a program's own)
// is refused before anything runs.
func openURL(goos, raw string, start func(name string, args ...string) error) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("only an http or https address is opened")
	}
	switch goos {
	case "windows":
		return start("rundll32", "url.dll,FileProtocolHandler", raw)
	case "darwin":
		return start("open", raw)
	}
	return start("xdg-open", raw)
}
