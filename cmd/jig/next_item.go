package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/mirror"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
)

// nextUnboundItem is `jig next <ref>` for a tracker item no ticket is bound
// to: it reads the item once and prints it, with the advisories that bear on
// binding it and the help that says what to do with it. It mints, writes and
// pushes nothing, and reads that one item only. The flags that ask about a
// ticket or a chart are refused before anything is read.
func nextUnboundItem(e env, stdout io.Writer, cfg project.Config, item *locate.Item, askedAboutTicket, jsonOut bool) int {
	if askedAboutTicket {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("%s is an unbound tracker item, not a ticket or a chart", item.Ref),
			Code: "VALIDATION_ERROR",
			Help: []string{"--for, --topology and --tickets ask about a ticket or a chart: run `jig next " + itemAsk(item) + "` with no flag to read the item"},
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), chartNewItemTimeout)
	defer cancel()
	detail, err := mirror.ReadItem(ctx, mirror.Deps{Cfg: cfg, Client: e.mirrorClient}, item.Repo, item.Number)
	if err != nil {
		return renderErr(stdout, locate.AddHelp(unreadableItem(item, err), item.Shadow))
	}

	view := newUnboundView(item, detail)
	if jsonOut {
		data, err := json.Marshal(view)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	axi.Render(stdout, view.blocks()...)
	return 0
}

// unreadableItem is the refusal for an item that could not be read: not the
// not-found one, which is the client's own, with its code. It is exit 1 and
// names the cause; `jig here` needs no network.
func unreadableItem(item *locate.Item, err error) error {
	var ae *axi.Error
	if errors.As(err, &ae) && ae.Code == "ITEM_NOT_FOUND" {
		return err
	}
	cause := err.Error()
	if errors.As(err, &ae) {
		cause = ae.Msg
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("item %s of %s could not be read: %s", item.Ref, item.Repo, cause),
		Code: "ITEM_UNREADABLE",
		Help: []string{
			"Run `gh auth login`, or set GH_TOKEN, and run it again",
			fmt.Sprintf("`jig here %s` needs no network", itemAsk(item)),
		},
	}
}

// unboundView is what `jig next <ref>` says of an item no ticket is bound to.
type unboundView struct {
	Ref        string        `json:"ref"`
	Repo       string        `json:"repo"`
	Bound      itemBoundJSON `json:"bound"`
	URL        string        `json:"url"`
	Title      string        `json:"title"`
	State      string        `json:"state"`
	Labels     []string      `json:"labels"`
	LinkedPRs  int           `json:"linked_prs"`
	Author     string        `json:"author"`
	Body       string        `json:"body"`
	Advisories []string      `json:"advisories"`
	Help       []string      `json:"help"`
}

func newUnboundView(item *locate.Item, d github.ItemDetail) unboundView {
	v := unboundView{
		Ref: item.Ref, Repo: item.Repo, Bound: itemBoundOf(item), URL: d.URL, Title: d.Title,
		State: "open", Labels: append([]string{}, d.Labels...), LinkedPRs: d.LinkedPullRequests, Author: d.Author, Body: d.Body,
		Advisories: []string{},
	}
	// The bind refuses an answer that is not the item asked for: GitHub
	// answers a transferred issue as it is now, where it moved to.
	moved := d.Number != item.Number || !strings.EqualFold(d.Repo, item.Repo)
	bindable := !item.Other && !moved
	if !d.Open {
		v.State = "closed"
		if reason := strings.ToLower(strings.ReplaceAll(d.StateReason, "_", " ")); reason != "" {
			v.State += " (" + reason + ")"
		}
		if bindable {
			v.Advisories = append(v.Advisories, fmt.Sprintf("%s is closed: the first sync after it is bound reopens it", item.Ref))
		}
	}
	if d.LinkedPullRequests > 0 {
		v.Advisories = append(v.Advisories, fmt.Sprintf("%s has %d linked pull request(s): check that the work is not already done", item.Ref, d.LinkedPullRequests))
	}
	if item.Shadow != "" {
		v.Advisories = append(v.Advisories, item.Shadow)
	}
	switch {
	case moved:
		v.Advisories = append(v.Advisories, fmt.Sprintf("%s of %s was moved: GitHub answers it as %s#%d (%s), which the bind refuses", item.Ref, item.Repo, d.Repo, d.Number, d.URL))
		v.Help = []string{fmt.Sprintf("Run intake with the moved item (%s) as its need; it cannot be bound to this project", d.URL)}
	case item.Other:
		v.Advisories = append(v.Advisories, fmt.Sprintf("%s is in %s, the project's tracker is %s: it can be read as input but not bound", item.Ref, item.Repo, item.TrackerRepo))
		v.Help = []string{fmt.Sprintf("Run intake with tracker item %s of %s as its need; it cannot be bound to this project", item.Ref, item.Repo)}
	default:
		v.Help = []string{
			fmt.Sprintf("Run intake with tracker item %s as its need: mint nothing before the verdict (jig skill intake)", item.Ref),
			fmt.Sprintf("The verdict makes its one issue and chart: jig chart new <name> --item %s", item.Ref),
			fmt.Sprintf("Once bound, jig next %s answers as the issue does", item.Ref),
		}
	}
	return v
}

// blocks renders the view: the next block, the body as a block of its own
// (each line indented two spaces, the text itself unchanged), the advisories
// and the help.
func (v unboundView) blocks() []string {
	labels := "-"
	if len(v.Labels) > 0 {
		labels = strings.Join(v.Labels, "+")
	}
	blocks := []string{axi.KV("next", [][2]string{
		{"ref", v.Ref}, {"repo", v.Repo}, {"bound", "no"}, {"url", v.URL}, {"title", v.Title},
		{"state", v.State}, {"labels", labels}, {"linked_prs", strconv.Itoa(v.LinkedPRs)}, {"author", v.Author},
	})}
	if body := strings.TrimRight(v.Body, "\n"); body == "" {
		blocks = append(blocks, "body: none")
	} else {
		b := axi.Block{Header: "body:"}
		for _, l := range strings.Split(body, "\n") {
			b.Lines = append(b.Lines, "  "+l)
		}
		blocks = append(blocks, b.String())
	}
	if len(v.Advisories) > 0 {
		b := axi.Block{Header: fmt.Sprintf("advisories[%d]:", len(v.Advisories))}
		for _, a := range v.Advisories {
			b.Lines = append(b.Lines, "  "+a)
		}
		blocks = append(blocks, b.String())
	}
	return append(blocks, axi.Help(v.Help...))
}
