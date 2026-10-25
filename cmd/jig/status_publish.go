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
//
// A ticket that publishes in several repos has one pull request for each, and
// its lines name their repo. The block then carries the ticket, the branch
// (one name in every repo) and the changelog, and a table beside it has a row
// for each repo:
//
//	publish_prs[2]{repo,pr,state,head,body_file}:
//	  api,https://example.test/acme/api/pull/7,opened,<sha>,tickets/JIG-2/pr/api.md
//	  web,-,-,<sha>,tickets/JIG-2/pr/web.md
//
// A row's pr and state are "-" until the repo's pull request is opened or
// recorded.
func publishBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	var body, approved, pr journal.Line
	var repos []string
	byRepo := map[string]*repoPublish{}
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
		default:
			continue
		}
		if l.Repo == "" {
			continue
		}
		rp := byRepo[l.Repo]
		if rp == nil {
			rp = &repoPublish{name: l.Repo}
			byRepo[l.Repo] = rp
			repos = append(repos, l.Repo)
		}
		switch l.Event {
		case journal.EventPrBody:
			rp.body = l
		case journal.EventPublishApproved:
			rp.approved = l
		case journal.EventPr:
			if l.URL != "" {
				rp.pr = l
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
	several := len(repos) > 1
	if !several {
		add("pr", pr.URL)
		add("pr_state", pr.Outcome)
		add("head", firstNonEmptyString(pr.Commit, approved.Commit, body.Commit))
	}
	add("branch", firstNonEmptyString(pr.Branch, approved.Branch, body.Branch))
	if path, ok := inStore(st.Root, body.Ref); ok && !several {
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
	blocks := []string{axi.KV("publish", pairs)}
	if several {
		rows := make([][]string, 0, len(repos))
		for _, name := range repos {
			rp := byRepo[name]
			bodyFile := "-"
			if _, ok := inStore(st.Root, rp.body.Ref); ok {
				bodyFile = rp.body.Ref
			}
			rows = append(rows, []string{
				name, dashed(rp.pr.URL), dashed(rp.pr.Outcome),
				dashed(firstNonEmptyString(rp.pr.Commit, rp.approved.Commit, rp.body.Commit)), bodyFile,
			})
		}
		blocks = append(blocks, axi.Table("publish_prs", []string{"repo", "pr", "state", "head", "body_file"}, rows))
	}
	return blocks, nil
}

// repoPublish is the publish lines of one repo of a ticket that publishes in
// several: the latest of its body, its approval and its pull request.
type repoPublish struct {
	name               string
	body, approved, pr journal.Line
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
