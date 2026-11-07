package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/store"
)

// How a pull request's What stood at a publish (WhatReport.Status): given
// with `jig publish --what`, taken from an earlier one that did, or still to
// be written, when the body carries the ticket's title as its one bullet.
const (
	WhatGiven   = "given"
	WhatStored  = "stored"
	WhatPending = "pending"
)

// WhatReport is what a publish says of a repo's What.
type WhatReport struct {
	Status string
	// Request is the file the session writes its bullets to while the What is
	// pending, and gives with `jig publish --what <file>`. "" when this machine
	// has no jig home to put it in.
	Request string
}

// loadWhatFile reads the file `jig publish --what` names: the bullets the
// session wrote for the pull request's What, or nil for no file. A file with
// nothing in it is refused, since it would leave a body with an empty What.
func loadWhatFile(path string) (*string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &axi.Error{Msg: fmt.Sprintf("the what file is unreadable: %v", err), Code: "WHAT_UNREADABLE"}
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if text == "" {
		return nil, &axi.Error{
			Msg:  "the what file is empty",
			Code: "WHAT_EMPTY",
			Help: []string{"Write a few bullets, the first saying why the change exists, and give the file again"},
		}
	}
	return &text, nil
}

// whatStorePath is where the ticket keeps the What of the pull request of
// repo: beside its body, in the ticket's pr directory.
func whatStorePath(st *store.Store, ticket, repo string) string {
	return filepath.Join(st.TicketDir(ticket), "pr", repo+".what.md")
}

// whatRequestPath is the file a pending What asks the session to write, or ""
// when it cannot be told (no jig home, no store id).
func whatRequestPath(d Deps, ticket, repo string) string {
	if d.Home == "" {
		return ""
	}
	id, err := d.Store.ID()
	if err != nil {
		return ""
	}
	path, err := home.WhatPath(d.Home, id, ticket, repo)
	if err != nil {
		return ""
	}
	return absPath(path)
}

// resolveWhat settles the What of one repo's pull request: the bullets given
// now, which the ticket stores; else the ones it stored, which a later render
// reuses; else, until the session writes them, one bullet with the ticket's
// title so a body is never empty, with the file to write asked for.
func resolveWhat(d Deps, ticket, repo, title string, given *string, warn func(format string, args ...any)) (string, WhatReport, error) {
	path := whatStorePath(d.Store, ticket, repo)
	if given != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", WhatReport{}, fmt.Errorf("verifydeliver: publish: what: create dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(*given+"\n"), 0o644); err != nil {
			return "", WhatReport{}, fmt.Errorf("verifydeliver: publish: what: write: %w", err)
		}
		return *given, WhatReport{Status: WhatGiven}, nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if text := strings.TrimSpace(string(data)); text != "" {
			return text, WhatReport{Status: WhatStored}, nil
		}
	} else if !os.IsNotExist(err) {
		return "", WhatReport{}, fmt.Errorf("verifydeliver: publish: what: read: %w", err)
	}
	report := WhatReport{Status: WhatPending, Request: whatRequestPath(d, ticket, repo)}
	if report.Request != "" {
		if err := os.MkdirAll(filepath.Dir(report.Request), 0o755); err != nil {
			return "", WhatReport{}, fmt.Errorf("verifydeliver: publish: what: create dir: %w", err)
		}
		warn("jig: the What of the pull request for %s is waiting: write a few bullets, the first saying why the change exists, to %s and give it with `jig publish --what <file>`\n", ticket, report.Request)
	} else {
		warn("jig: the What of the pull request for %s is waiting: give a few bullets, the first saying why the change exists, with `jig publish --what <file>`\n", ticket)
	}
	return "- " + title, report, nil
}
