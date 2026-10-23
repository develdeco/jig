package setup

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The hook command contract. Everything jig puts into a harness's hook
// configuration, and into a git hook, is a command line that begins with the
// jig program and then one of its hook verbs:
//
//	jig observe --harness claude        a harness event, read on stdin
//	jig githook post-commit             a git hook
//
// The program is the bare word jig when the jig on PATH is the running binary,
// and the quoted absolute path of the running binary otherwise (JigCommand).

// ownedCommandRe says which commands are jig's. It is a detection, not a
// record: jig does not remember what it wrote, it recognizes it, so an entry
// written by an older jig, from another path or by hand is still found and
// still removed. Anything else in a hook configuration is the person's and is
// never touched.
//
// The design's expression is ^"?[^"]*jig(\.exe)?"? (observe|githook)\b. This is
// the same, with the program held to one word of the command: a bare jig, or
// a path whose last element is jig or jig.exe, quoted (and then it may hold
// spaces) or not. That keeps a command that only mentions jig on its way to
// something else (echo ran jig observe) out of reach of an uninstall.
var ownedCommandRe = regexp.MustCompile(`^(?:"(?:[^"]*[/\\])?jig(?:\.exe)?"|(?:[^\s"]*[/\\])?jig(?:\.exe)?) (?:observe|githook)\b`)

// OwnedCommand reports whether command is one jig installed: its program is a
// binary named jig (bare, or by a path, quoted or not) and its first argument
// is observe or githook.
func OwnedCommand(command string) bool {
	return ownedCommandRe.MatchString(command)
}

// JigCommand returns the program part of a hook command: the bare word jig
// when onPath (the jig a shell finds is the running binary, so the entry
// keeps working when the binary moves or is upgraded in place), otherwise the
// absolute path of executable in double quotes, with forward slashes (Git
// Bash, which runs hooks on Windows, reads both). It refuses a binary that is
// not named jig or jig.exe, since OwnedCommand could not recognize its
// entries again, and a path no double-quoted shell word can carry.
func JigCommand(executable string, onPath bool) (string, error) {
	base := strings.ToLower(filepath.Base(executable))
	if base != "jig" && base != "jig.exe" {
		return "", fmt.Errorf("the running binary is named %q; hook entries are recognized by the name jig, so run it as jig", filepath.Base(executable))
	}
	if onPath {
		return "jig", nil
	}
	if !filepath.IsAbs(executable) {
		return "", fmt.Errorf("the running binary %q is not an absolute path", executable)
	}
	p := filepath.ToSlash(executable)
	if strings.ContainsAny(p, "\"$`\\\r\n") {
		return "", fmt.Errorf("the path of the running binary %q has a character a hook command cannot quote", executable)
	}
	return `"` + p + `"`, nil
}
