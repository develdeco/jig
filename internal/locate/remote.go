package locate

import (
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// scpRemote matches git's scp-like remote syntax, [user@]host:path, whose host
// is more than one character (so a Windows drive letter, C:/x, is a path).
var scpRemote = regexp.MustCompile(`^(?:[^@/\\:]+@)?([^/\\:]{2,}):(.+)$`)

// NormalizeRemote reduces a git remote to a comparable form, so the spellings
// of one repository agree: https://github.com/o/r.git, git@github.com:o/r and
// ssh://git@github.com/o/r/ all become "github.com/o/r". The host and path of
// a URL are lower-cased (hosting services ignore case); a local path (or a
// file:// URL) keeps its case except on Windows, and has its separators made
// forward slashes. A trailing ".git" and slashes are dropped. "" stays "".
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return localForm(s)
		}
		if u.Scheme == "file" {
			p := u.Path
			// file:///D:/x is the path /D:/x.
			if len(p) > 2 && p[0] == '/' && p[2] == ':' {
				p = p[1:]
			}
			return localForm(p)
		}
		return urlForm(u.Hostname(), u.Path)
	}
	if m := scpRemote.FindStringSubmatch(s); m != nil {
		return urlForm(m[1], m[2])
	}
	return localForm(s)
}

func urlForm(host, path string) string {
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	path = strings.Trim(path, "/")
	return strings.ToLower(host + "/" + path)
}

func localForm(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimSuffix(strings.TrimRight(p, "/"), ".git")
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// isRemoteURL reports whether s is a URL or an scp-like remote, as opposed to
// a path on this machine.
func isRemoteURL(s string) bool {
	return strings.Contains(s, "://") || scpRemote.MatchString(s)
}
