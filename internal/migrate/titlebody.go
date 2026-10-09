package migrate

import (
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// resolveTitleBody resolves ticket's title and description on the v1 store,
// each from the first of these that has it, the same rank order
// internal/mirror's ResolveTicketTitleBody applies on a schema-2 store, plus
// the one rank that only ever matters here, on the v1 store this reads
// before deleting tracker/ticket.md (brief.md#The migration step 4, "its
// tracker/ticket.md history step moves from the mirror into the
// migration"):
//  1. its ticket.yaml (title:, body:);
//  2. its entry in a chart's tickets.yaml (title:, body:);
//  3. the latest committed <ticket>/tracker/ticket.md whose commit is not
//     publish's (`<id>: publish`): a "# " first line is the title, the
//     rest the description;
//  4. the ticket's id, as the title, with no description.
func resolveTitleBody(st *store.Store, ticket string) (title, body string, err error) {
	rec, err := store.ReadTicketFile(filepath.Join(oldTicketDir(st.Root, ticket), "ticket.yaml"))
	if err != nil {
		return "", "", err
	}
	title, body = rec.Title, rec.Body

	if title == "" || body == "" {
		entry, found, err := findChartEntry(st, ticket)
		if err != nil {
			return "", "", err
		}
		if found {
			if title == "" {
				title = entry.Title
			}
			if body == "" {
				body = entry.Body
			}
		}
	}

	if title == "" || body == "" {
		mdTitle, mdBody, found, err := latestTicketMD(st, ticket)
		if err != nil {
			return "", "", err
		}
		if found {
			if title == "" {
				title = mdTitle
			}
			if body == "" {
				body = mdBody
			}
		}
	}

	if title == "" {
		title = ticket
	}
	return title, body, nil
}

// findChartEntry looks for ticket among every chart's tickets.yaml, in
// chart name order, returning the first match.
func findChartEntry(st *store.Store, ticket string) (store.ChartEntry, bool, error) {
	names, err := st.ChartNames()
	if err != nil {
		return store.ChartEntry{}, false, err
	}
	for _, name := range names {
		entries, err := st.ReadChart(name)
		if err != nil {
			return store.ChartEntry{}, false, err
		}
		for _, e := range entries {
			if e.ID == ticket {
				return e, true, nil
			}
		}
	}
	return store.ChartEntry{}, false, nil
}

// latestTicketMD reads the latest committed <ticket>/tracker/ticket.md
// whose commit is not publish's own (`<id>: publish`). found is false when
// there is no such commit: no commit ever touched the path, or every one
// that did was publish's.
func latestTicketMD(st *store.Store, ticket string) (title, body string, found bool, err error) {
	relPath := filepath.Join(ticket, "tracker", "ticket.md")
	out, gerr := gitx.Run(st.Root, "log", "--format=%H%x1f%s", "--", relPath)
	if gerr != nil || strings.TrimSpace(out) == "" {
		return "", "", false, nil
	}
	publishSubject := ticket + ": publish"
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		sha, subject, ok := strings.Cut(line, "\x1f")
		if !ok || subject == publishSubject {
			continue
		}
		content, serr := gitx.Run(st.Root, "show", sha+":"+filepath.ToSlash(relPath))
		if serr != nil {
			continue
		}
		first, rest, _ := strings.Cut(content, "\n")
		if !strings.HasPrefix(first, "# ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(first, "# ")), strings.TrimSpace(rest), true, nil
	}
	return "", "", false, nil
}
