package verifydeliver

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/session"
)

// hostDir is a directory of this machine that a refusal reason must not
// print by its path, and the name it prints instead.
type hostDir struct {
	dir  string
	name string
}

// hostPathSpelling pairs one literal spelling of a hostDir with the name a
// caller should use for it: leaveOutHostPaths' own replacement, or
// containsHostPath's report of which directory matched.
type hostPathSpelling struct{ text, name string }

// hostPathSpellings returns every spelling of dirs a caller should treat as
// naming this machine's own directories: as each is given (when absolute),
// its absolute form, each with forward slashes, and each Go-quoted (the way
// %q prints a path, where a Windows path's backslashes double) - and, when
// withWSL, each one's WSL mount spelling too (session.WSLPath): the
// respelling herdr hands a session on Windows (session.respellMentions), a
// spelling jig chose for the session rather than jig's own text, so only a
// caller asking what a session might have repeated needs it. A bare
// relative dir names nothing of the machine and yields nothing, as does an
// empty directory (which would resolve to the working directory) or a
// filesystem root, a prefix of every path that would match them all.
// Longest spelling first, so a directory nested inside another (the picks
// directory under the jig home) is matched, and named, as itself.
func hostPathSpellings(withWSL bool, dirs ...hostDir) []hostPathSpelling {
	var all []hostPathSpelling
	seen := map[string]bool{}
	add := func(text, name string) {
		if text == "" || seen[text] {
			return
		}
		seen[text] = true
		all = append(all, hostPathSpelling{text, name})
	}
	for _, d := range dirs {
		if d.dir == "" {
			continue
		}
		abs := absPath(d.dir)
		for _, text := range []string{d.dir, abs, filepath.ToSlash(d.dir), filepath.ToSlash(abs)} {
			// A filesystem root is a prefix of every path, and naming it would
			// match all of them.
			if !filepath.IsAbs(text) || filepath.Dir(text) == text {
				continue
			}
			quoted := strconv.Quote(text)
			add(text, d.name)
			add(quoted[1:len(quoted)-1], d.name)
		}
		if withWSL {
			// session.WSLPath is a no-op off a Windows-drive path, so a spelling
			// that comes back unchanged is already covered by the raw spellings
			// above, and jig never hands out a bare "/" as one of its own
			// directories for this to wrongly match everything.
			for _, text := range []string{session.WSLPath(d.dir), session.WSLPath(abs)} {
				if text != d.dir && text != abs {
					add(text, d.name)
				}
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return len(all[i].text) > len(all[j].text) })
	return all
}

// leaveOutHostPaths returns s with every spelling of each directory
// (hostPathSpellings, without the WSL mount form: this composes jig's own
// refusal reasons, never a session's text) replaced by its name. A refusal
// reason is committed to the store and printed, and the operating system
// errors it carries name the path they failed on, which for the jig home
// holds the operator's user name. It is for the paths jig chose. A path the
// session chose is never put in a reason, and the text of a failure of the
// session or its backend, which can spell a path any way it likes, is never
// recorded (pickFailed).
func leaveOutHostPaths(s string, dirs ...hostDir) string {
	for _, sp := range hostPathSpellings(false, dirs...) {
		s = strings.ReplaceAll(s, sp.text, sp.name)
	}
	return s
}

// containsHostPath reports whether s literally contains one of dirs' own
// spellings, including every spelling jig may have handed a session
// (hostPathSpellings' WSL mount form among them): the render-time check
// behind the owner's decision on r1-f13 (DECISIONS.md) that a caption or a
// summary naming one of jig's own directories is left out of a published
// pull request body rather than rendered verbatim. Comparison is
// exact-string only, never a pattern: jig does not try to recognize a path
// shape it did not itself hand out.
func containsHostPath(s string, dirs ...hostDir) bool {
	for _, sp := range hostPathSpellings(true, dirs...) {
		if strings.Contains(s, sp.text) {
			return true
		}
	}
	return false
}
