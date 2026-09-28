package intent

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Match is the chosen session plus its file-overlap score.
type Match struct {
	Session *Session
	Score   float64
	// Overlap is the diffFiles Best was given that this session mentions,
	// in diffFiles order.
	Overlap []string
}

// Best picks the best-matching session for diffFiles - a gate round's own
// scope diff, non-deleted files only - among sessions, or nil when none
// clears the acceptance bar. headTime anchors the staleness rule; the zero
// time disables it. Ties (equal score) break toward the more recently
// active session.
//
// This is one generic algorithm, advisory: it only ever picks which
// session a model then summarizes, never judges the change or the
// session's own prose.
func Best(sessions []*Session, diffFiles []string, headTime time.Time) *Match {
	var candidates []*Match
	for _, s := range sessions {
		score, overlap := scoreSession(s, diffFiles)
		if !accepted(score, len(overlap), len(diffFiles), s.LastActivity, headTime) {
			continue
		}
		candidates = append(candidates, &Match{Session: s, Score: score, Overlap: overlap})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Session.LastActivity.After(candidates[j].Session.LastActivity)
	})
	return candidates[0]
}

// accepted applies the acceptance rules: at least one overlapping file; a
// multi-file diff needs at least 2 overlapping files and a score of at
// least 0.5; a session last active more than 24 hours before headTime
// needs a score of at least 0.8 instead.
func accepted(score float64, overlapCount, diffCount int, lastActivity, headTime time.Time) bool {
	if diffCount == 0 || overlapCount == 0 {
		return false
	}
	if diffCount > 1 && overlapCount < 2 {
		return false
	}
	if score < 0.5 {
		return false
	}
	if !headTime.IsZero() && !lastActivity.IsZero() && lastActivity.Before(headTime.Add(-24*time.Hour)) && score < 0.8 {
		return false
	}
	return true
}

// scoreSession computes the share of diffFiles that s mentions. Every
// mention and every diffFile is normalized exactly once up front (into a
// deduplicated set for the mentions), not once per (mention, diffFile)
// pair as a naive nested loop would: a session with many turns can carry
// tens of thousands of raw mention candidates, and re-parsing each one for
// every diff file made this the dominant cost of a large gate round's own
// inference attempt.
func scoreSession(s *Session, diffFiles []string) (float64, []string) {
	if len(diffFiles) == 0 {
		return 0, nil
	}
	mentionSet := map[string]struct{}{}
	for _, m := range sessionMentions(s) {
		if norm := normalizeMention(m); norm != "" {
			mentionSet[norm] = struct{}{}
		}
	}
	var overlap []string
	for _, f := range diffFiles {
		d := normalizeMention(f)
		if d == "" {
			continue
		}
		for m := range mentionSet {
			if normalizedMentionMatches(m, d) {
				overlap = append(overlap, f)
				break
			}
		}
	}
	return float64(len(overlap)) / float64(len(diffFiles)), overlap
}

// sessionMentions collects every candidate file mention from s: each
// message's own tool-call file paths (relativized against the session's
// repo root) plus a structural scan of its text for path-like tokens.
func sessionMentions(s *Session) []string {
	var out []string
	for _, m := range s.Messages {
		for _, p := range m.FilePaths {
			out = append(out, relativizeToCWD(p, s.CWD, s.CWDTopLevel))
		}
		out = append(out, scanFilePathsInText(m.Text)...)
	}
	return out
}

// relativizeToCWD turns an absolute tool-call path inside the session's own
// repo into a repo-relative one, matching a diff file's own shape (diff
// files are always repo-root relative, from git itself). It relativizes
// against topLevel, the session's own git worktree top level, when known,
// falling back to cwd only when topLevel could not be resolved: cwd is
// often a subdirectory a session happened to start in, and relativizing an
// absolute path against it instead would turn a path outside cwd but still
// inside the repo (or, worse, a path directly in cwd itself) into a bare
// basename or a "../"-relative one, neither of which mentionMatches can
// still recognize - the underlying file just goes unmentioned instead.
// Anything else - already relative, or absolute outside the repo entirely -
// passes through unchanged so mentionMatches still gets a chance at it via
// its own suffix rule.
func relativizeToCWD(p, cwd, topLevel string) string {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return p
	}
	base := topLevel
	if base == "" {
		base = cwd
	}
	if base == "" {
		return p
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return p
	}
	return rel
}

// mentionMatches reports whether mention names diffFile: equal, or a
// "/"-bounded suffix of it - except a bare-basename diffFile (no directory
// component, e.g. a repo-root file), which only a bare mention (no
// directory component of its own) can name, so a nested file sharing that
// basename never falsely stands in for the repo-root one.
func mentionMatches(mention, diffFile string) bool {
	return normalizedMentionMatches(normalizeMention(mention), normalizeMention(diffFile))
}

// normalizedMentionMatches is mentionMatches's own rule, given m and d
// already normalized (normalizeMention) by the caller - scoreSession's own
// hot loop, which normalizes once per distinct string rather than once per
// (mention, diffFile) pair.
func normalizedMentionMatches(m, d string) bool {
	if m == "" || d == "" {
		return false
	}
	if m == d {
		return true
	}
	if !strings.Contains(d, "/") {
		return false
	}
	return strings.HasSuffix(m, "/"+d)
}

// normalizeMention puts p into the slash-separated, "./"-stripped, cleaned
// form both a diff file and a mention are compared in.
func normalizeMention(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return path.Clean(p)
}

// filePathTokens is a permissive, structural scan for path-like tokens in
// prose: it widens the candidate mention set, never judges the prose
// itself - a false positive costs nothing, since mentionMatches still has
// to agree it names a real diff file.
var filePathTokens = regexp.MustCompile(`[A-Za-z0-9_.\-/\\]+\.[A-Za-z0-9]+`)

func scanFilePathsInText(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, tok := range filePathTokens.FindAllString(text, -1) {
		tok = strings.Trim(tok, "\"'`,;:()[]{}<>")
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}
