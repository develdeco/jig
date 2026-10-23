package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
)

// A file jig puts into a repository for one person's use (a settings.local.json
// with hook entries, a stub of a skill) must not show up in git status or get
// committed by a git add -A. Each repository has a place for exactly that, its
// own .git/info/exclude, which nothing shares. jig keeps one block in it,
// bracketed like the one in a hook, and lists its files there.

// ExcludeFile has the repository that holds file ignore it, with a line in the
// jig block of its .git/info/exclude. A file that is in no repository is
// nothing to exclude. It is idempotent, and a line already in the file outside
// jig's block is left as the person's own.
func ExcludeFile(file string) (changed bool, err error) {
	exclude, pattern, ok, err := excludeTarget(file)
	if err != nil || !ok {
		return false, err
	}
	data, _, err := readIfExists(exclude)
	if err != nil {
		return false, err
	}
	out, changed, err := editExclude(data, pattern, true)
	if err != nil || !changed {
		return false, err
	}
	return true, writeFile(exclude, out, 0o644, true)
}

// UnexcludeFile takes file's line out of the jig block, and the block with it
// once it lists nothing.
func UnexcludeFile(file string) (changed bool, err error) {
	exclude, pattern, ok, err := excludeTarget(file)
	if err != nil || !ok {
		return false, err
	}
	data, exists, err := readIfExists(exclude)
	if err != nil || !exists {
		return false, err
	}
	out, changed, err := editExclude(data, pattern, false)
	if err != nil || !changed {
		return false, err
	}
	return true, writeFile(exclude, out, 0o644, true)
}

// excludeTarget finds the exclude file of the repository holding file and the
// pattern that names file in it, anchored at the top of the work tree.
func excludeTarget(file string) (exclude, pattern string, ok bool, err error) {
	// The directories the file will sit in may not exist yet: stand on the
	// nearest one that does, and keep the rest of the path to put back.
	dir, missing := filepath.Dir(file), filepath.Base(file)
	for {
		if info, serr := os.Stat(dir); serr == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		missing = filepath.Join(filepath.Base(dir), missing)
		dir = parent
	}
	top, err := gitx.TopLevel(dir)
	if err != nil {
		return "", "", false, nil // not in a repository: nothing to exclude from
	}
	// The file may sit under a symlinked spelling of the top; resolve the part
	// of its path that exists so the two compare.
	resolved := dir
	if r, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		resolved = r
	}
	rel, rerr := filepath.Rel(top, filepath.Join(resolved, missing))
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false, nil
	}
	path, err := gitx.Run(dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", "", false, fmt.Errorf("find the exclude file of %s: %w", dir, err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return path, "/" + filepath.ToSlash(rel), true, nil
}

// editExclude adds pattern to the jig block of an exclude file's data (add) or
// removes it (not add), and reports whether it changed anything.
func editExclude(data []byte, pattern string, add bool) ([]byte, bool, error) {
	begin, end, found, err := findBlockBetween(data, blockBegin, blockEnd)
	if err != nil {
		return nil, false, err
	}
	var listed []string
	if found {
		for _, line := range strings.Split(strings.TrimRight(string(data[begin:end]), "\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if line != blockBegin && line != blockEnd && line != "" {
				listed = append(listed, line)
			}
		}
	}
	has := false
	for _, l := range listed {
		has = has || l == pattern
	}
	switch {
	case add && has, !add && !has:
		return data, false, nil
	case add:
		// A line of the person's own that already says it is enough.
		outside := string(data)
		if found {
			outside = string(data[:begin]) + string(data[end:])
		}
		for _, line := range strings.Split(outside, "\n") {
			if strings.TrimRight(line, "\r") == pattern {
				return data, false, nil
			}
		}
		listed = append(listed, pattern)
	default:
		kept := listed[:0]
		for _, l := range listed {
			if l != pattern {
				kept = append(kept, l)
			}
		}
		listed = kept
	}

	var block string
	if len(listed) > 0 {
		block = blockBegin + "\n" + strings.Join(listed, "\n") + "\n" + blockEnd + "\n"
	}
	switch {
	case found:
		return append(append(append([]byte{}, data[:begin]...), block...), data[end:]...), true, nil
	default:
		out := append([]byte{}, data...)
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		return append(out, block...), true, nil
	}
}
