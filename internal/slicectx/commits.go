package slicectx

import (
	"os"
	"regexp"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// sliceTrailerKey is the commit trailer that names the slice a commit is for.
const sliceTrailerKey = "Jig-Slice"

// sliceTrailerRE reads a Jig-Slice trailer line.
var sliceTrailerRE = regexp.MustCompile(`^` + sliceTrailerKey + `:[ \t]*(\S+)[ \t]*$`)

// maxScanned bounds how many commits are read when the ticket has no start
// sha in the clone to bound the range by.
const maxScanned = "300"

// builtCommit is a commit found in the clone that carries a Jig-Slice
// trailer, with the files it changed.
type builtCommit struct {
	sha, subject string
	files        []string
}

// earlierSlices lists the slices of the ticket whose green jig verified, other
// than the one the context is for, in the order they first verified. Each one
// gets the commits of the clone that carry its Jig-Slice trailer, oldest
// first, and the files those changed. A slice none of whose commits the clone
// holds (it was built in another repo, or the history was rewritten) is
// listed with the commits its verified lines name, and no files.
func earlierSlices(in Input, lines []journal.Line) []EarlierSlice {
	out := []EarlierSlice{}
	verified := journal.VerifiedSlices(lines, in.Slice)
	if len(verified) == 0 {
		return out
	}
	built := commitsBySlice(in)
	for _, v := range verified {
		e := EarlierSlice{ID: v.Slice, Commits: []Commit{}, Files: []string{}}
		seenFile := map[string]bool{}
		for _, c := range built[v.Slice] {
			e.Commits = append(e.Commits, Commit{SHA: c.sha, Subject: c.subject})
			for _, f := range c.files {
				if seenFile[f] {
					continue
				}
				seenFile[f] = true
				if len(e.Files) == MaxEarlierFiles {
					e.FilesOmitted++
					continue
				}
				e.Files = append(e.Files, f)
			}
		}
		if len(e.Commits) == 0 {
			seenCommit := map[string]bool{}
			for _, l := range lines {
				if l.Event == journal.EventVerified && l.Slice == v.Slice && l.Commit != "" && !seenCommit[l.Commit] {
					seenCommit[l.Commit] = true
					e.Commits = append(e.Commits, Commit{SHA: l.Commit})
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// commitsBySlice reads the commits of the clone since the ticket start sha in
// it (else the latest few hundred) and groups those with a Jig-Slice trailer
// by slice, oldest first. A clone that is absent, or that git cannot read,
// has none: the context is then the less for it, not refused.
func commitsBySlice(in Input) map[string][]builtCommit {
	if in.RepoDir == "" {
		return nil
	}
	args := []string{"-c", "core.quotePath=false", "log", "--no-merges", "--name-only", "--reverse",
		"--format=%x1e%H%x1f%s%x1f%B%x1f"}
	if start := startSHA(in); start != "" {
		args = append(args, start+"..HEAD")
	} else {
		args = append(args, "-n", maxScanned, "HEAD")
	}
	raw, err := gitx.Run(in.RepoDir, args...)
	if err != nil {
		return nil
	}
	return groupByTrailer(raw)
}

// startSHA is the start sha the ticket recorded for the repo, when the clone
// holds that commit; "" otherwise.
func startSHA(in Input) string {
	if in.Repo == "" {
		return ""
	}
	data, err := os.ReadFile(in.Store.StartSHAPath(in.Ticket, in.Repo))
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(data))
	if sha == "" {
		return ""
	}
	if _, err := gitx.RevParse(in.RepoDir, sha+"^{commit}"); err != nil {
		return ""
	}
	return sha
}

// groupByTrailer parses the output of the git log in commitsBySlice: records
// start with 0x1e, and hold the sha, subject, message and (after the last 0x1f)
// the file names, which git prints below the message.
func groupByTrailer(raw string) map[string][]builtCommit {
	out := map[string][]builtCommit{}
	for _, rec := range strings.Split(raw, "\x1e") {
		parts := strings.SplitN(rec, "\x1f", 4)
		if len(parts) < 4 {
			continue
		}
		slice := sliceTrailer(parts[2])
		if slice == "" {
			continue
		}
		var files []string
		for _, f := range strings.Split(parts[3], "\n") {
			if f = strings.TrimSpace(f); f != "" {
				files = append(files, f)
			}
		}
		out[slice] = append(out[slice], builtCommit{sha: parts[0], subject: parts[1], files: files})
	}
	return out
}

// sliceTrailer is the slice named by the Jig-Slice trailer in the last
// paragraph of a commit message, "" when it has none.
func sliceTrailer(message string) string {
	message = strings.ReplaceAll(strings.TrimSpace(message), "\r\n", "\n")
	paragraph := message
	if i := strings.LastIndex(message, "\n\n"); i >= 0 {
		paragraph = message[i+2:]
	}
	slice := ""
	for _, line := range strings.Split(paragraph, "\n") {
		if m := sliceTrailerRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			slice = m[1]
		}
	}
	return slice
}
