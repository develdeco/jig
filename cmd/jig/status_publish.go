package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 220, Name: "publish", Blocks: publishBlocks})
}

// The most a publish block carries of the pull request body and of the
// changelog, so a status stays a summary: the first lines, each cut short. The
// files hold the rest.
const (
	publishTextLines = 8
	publishLineRunes = 160
)

// publishBlocks is the `publish` block of `jig status`: what a publish has
// written of the ticket, from its journal and the files it names.
//
//	publish:
//	  ticket: JIG-2
//	  pr: "https://example.test/acme/demo/pull/7"
//	  pr_state: opened
//	  head: <the reviewed head, 40 hex digits>
//	  branch: jig/JIG-2
//	  body_file: tickets/JIG-2/pr/demo.md
//	  body: "<the first lines of the pull request body>"
//	  changelog_file: tickets/JIG-2/changelog/consolidated.md
//	  changelog: "<the first lines of the consolidated changelog>"
//
// Each key is there once the publish has recorded it: the body once `jig
// publish --body` wrote it, the pull request once one was opened or recorded,
// the changelog once the publish finished. head is the head of the latest pr,
// publish-approved or pr-body line. A ticket whose publish has not begun has no
// block.
func publishBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	var body, approved, pr journal.Line
	for _, l := range lines {
		switch l.Event {
		case journal.EventPrBody:
			body = l
		case journal.EventPublishApproved:
			approved = l
		case journal.EventPr:
			if l.URL != "" {
				pr = l
			}
		}
	}
	if body.Event == "" && approved.Event == "" && pr.Event == "" {
		return nil, nil
	}
	pairs := [][2]string{{"ticket", ticket}}
	add := func(key, value string) {
		if value != "" {
			pairs = append(pairs, [2]string{key, value})
		}
	}
	add("pr", pr.URL)
	add("pr_state", pr.Outcome)
	add("head", firstNonEmptyString(pr.Commit, approved.Commit, body.Commit))
	add("branch", firstNonEmptyString(pr.Branch, approved.Branch, body.Branch))
	if path, ok := inStore(st.Root, body.Ref); ok {
		add("body_file", body.Ref)
		text, err := firstLinesOf(path, false)
		if err != nil {
			return nil, err
		}
		add("body", text)
	}
	rel := filepath.ToSlash(filepath.Join(st.TicketRelDir(ticket), "changelog", "consolidated.md"))
	changelog, err := firstLinesOf(filepath.Join(st.Root, filepath.FromSlash(rel)), true)
	if err != nil {
		return nil, err
	}
	if changelog != "" {
		add("changelog_file", rel)
		add("changelog", changelog)
	}
	return []string{axi.KV("publish", pairs)}, nil
}

// inStore is the path of the store-relative file rel names, and whether it lies
// inside the store: a journal line is shared by every writer of the store, and
// a path it names must not lead a status out of it.
func inStore(root, rel string) (string, bool) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", false
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(root, path)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

// firstLinesOf is the first publishTextLines lines of the file at path that say
// something, each cut at publishLineRunes, joined by newlines. skipTitle leaves
// out a markdown title line ("# ..."). A file that is not there reads as "".
func firstLinesOf(path string, skipTitle bool) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" || (skipTitle && strings.HasPrefix(line, "# ")) {
			continue
		}
		if utf8.RuneCountInString(line) > publishLineRunes {
			line = string([]rune(line)[:publishLineRunes-3]) + "..."
		}
		out = append(out, line)
		if len(out) == publishTextLines {
			break
		}
	}
	return strings.Join(out, "\n"), nil
}

// firstNonEmptyString is the first of ss that is not "".
func firstNonEmptyString(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
