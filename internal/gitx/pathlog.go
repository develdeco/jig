package gitx

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LogEntry is one commit of a path-limited log: its full hash, its committer
// time (the one CommitTime reads) and the first line of its message.
type LogEntry struct {
	SHA     string
	Time    time.Time
	Subject string
}

// PathLog returns the limit newest commits of rev's history that changed
// anything under paths, as git's path-limited log gives them: newest first,
// in git's own order, with git's default history simplification. It is one
// git process, since walking history with merge simplification is what the
// git program computes (see the note at the top of read.go). Each path is
// taken literally, not as a glob.
//
// No commit is nil with no error. A rev that starts with "-", a limit below
// 1 and no paths are refused, as Trailer refuses a revision that starts with
// "-": none of them reaches git.
func PathLog(dir, rev string, limit int, paths ...string) ([]LogEntry, error) {
	if strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("gitx: path log: %q is not a revision", rev)
	}
	if limit < 1 {
		return nil, fmt.Errorf("gitx: path log: limit %d is below 1", limit)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("gitx: path log: no paths")
	}
	args := []string{
		"--literal-pathspecs", "log", "-n", strconv.Itoa(limit), "--no-show-signature", "-z",
		"--format=%H%x1f%cI%x1f%s", rev, "--",
	}
	out, err := Run(dir, append(args, paths...)...)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, record := range strings.Split(out, "\x00") {
		if record = strings.TrimSpace(record); record == "" {
			continue
		}
		// The subject is the last field, so it takes the rest of the record.
		fields := strings.SplitN(record, "\x1f", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("gitx: path log: read %q: want a hash, a time and a subject", record)
		}
		at, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			return nil, fmt.Errorf("gitx: path log: parse commit time %q: %w", fields[1], err)
		}
		entries = append(entries, LogEntry{SHA: fields[0], Time: at, Subject: fields[2]})
	}
	return entries, nil
}
