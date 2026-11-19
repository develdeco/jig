package gitx

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RemovedSentence is one sentence a diff removes: Path is the file it left, as
// the old side names it, and Text the sentence, trimmed.
type RemovedSentence struct {
	Path string
	Text string
}

// RemovedSentences returns the sentences base..head removes in dir, in diff
// order, with renames off as in DiffAdded (a renamed file is a deletion and an
// addition). A removed line holds one sentence or several: each is cut after a
// ".", "!" or "?" that ends the line or is followed by a space, and is kept when
// it ends in one of them and has minWords or more words. A binary file and a
// submodule remove none. Whether a file's new version still holds a sentence is
// for the caller to ask.
func RemovedSentences(dir, base, head string, minWords int) ([]RemovedSentence, error) {
	args := []string{"-c", "core.quotepath=false", "diff", "-U0", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--ignore-submodules=all", "--src-prefix=a/", "--dst-prefix=b/", base, head}
	var stdout, stderr bytes.Buffer
	if err := run(dir, nil, false, &stdout, &stderr, args); err != nil {
		return nil, callError(args, stderr.String(), err)
	}
	var out []RemovedSentence
	path, inHeader := "", false
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path, inHeader = "", true
		case inHeader && strings.HasPrefix(line, "--- "):
			name, err := oldSidePath(line)
			if err != nil {
				return nil, fmt.Errorf("gitx: removed sentences: %w", err)
			}
			path = name
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
		case !inHeader && path != "" && strings.HasPrefix(line, "-"):
			for _, s := range splitSentences(line[1:], minWords) {
				out = append(out, RemovedSentence{Path: path, Text: s})
			}
		}
	}
	return out, nil
}

// oldSidePath reads a file header's "--- " line: the file's path before the
// change, or "" for /dev/null (a new file).
func oldSidePath(line string) (string, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(line, "--- "), "\t")
	if strings.HasPrefix(name, `"`) {
		unq, err := strconv.Unquote(name)
		if err != nil {
			return "", fmt.Errorf("unquote %s: %w", name, err)
		}
		name = unq
	}
	if name == "/dev/null" {
		return "", nil
	}
	return strings.TrimPrefix(name, "a/"), nil
}

// splitSentences cuts a line into the sentences that end in ".", "!" or "?"
// and have minWords or more words. A cut falls after one of those marks that
// ends the line or is followed by a space; text after the last mark is not a
// sentence.
func splitSentences(line string, minWords int) []string {
	var out []string
	start := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if (c != '.' && c != '!' && c != '?') || (i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t') {
			continue
		}
		s := strings.TrimSpace(line[start : i+1])
		start = i + 1
		if len(strings.Fields(s)) >= minWords {
			out = append(out, s)
		}
	}
	return out
}

// GrepHit is one line of a tracked file that holds a searched string: Path is
// repo-relative with forward slashes, Line the 1-based line and Text its
// content.
type GrepHit struct {
	Path string
	Line int
	Text string
}

// GrepFixed returns every line of the text files tracked at rev in dir that
// holds any of patterns verbatim, as fixed strings, in path order. A binary
// file is skipped, and no match is an empty result, not an error. rev is
// resolved to a commit first. An empty pattern is dropped: it would match every
// line.
func GrepFixed(dir, rev string, patterns []string) ([]GrepHit, error) {
	var kept []string
	for _, p := range patterns {
		if p != "" && !strings.ContainsAny(p, "\r\n") {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	full, err := Run(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("gitx: grep fixed: rev %q does not resolve to a commit: %w", rev, err)
	}
	full = strings.TrimSpace(full)
	f, err := os.CreateTemp("", "jig-grep-*")
	if err != nil {
		return nil, fmt.Errorf("gitx: grep fixed: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(strings.Join(kept, "\n") + "\n"); err != nil {
		f.Close()
		return nil, fmt.Errorf("gitx: grep fixed: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("gitx: grep fixed: %w", err)
	}
	args := []string{"-c", "core.quotepath=false", "grep", "-z", "-n", "-I", "-F", "--no-color", "--no-textconv",
		"-f", f.Name(), full, "--"}
	var stdout, stderr bytes.Buffer
	if err := run(dir, nil, false, &stdout, &stderr, args); err != nil {
		if stderr.Len() == 0 && stdout.Len() == 0 {
			return nil, nil // git grep exits 1 when nothing matches
		}
		return nil, callError(args, stderr.String(), err)
	}
	// With -z each hit is "<rev>:<path>\0<line>\0<text>\n".
	var hits []GrepHit
	for _, rec := range strings.Split(stdout.String(), "\n") {
		rec = strings.TrimSuffix(rec, "\r")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitx: grep fixed: unexpected output %q", rec)
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("gitx: grep fixed: unexpected line number in %q", rec)
		}
		hits = append(hits, GrepHit{Path: strings.TrimPrefix(parts[0], full+":"), Line: n, Text: parts[2]})
	}
	return hits, nil
}
