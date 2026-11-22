package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror"
)

// chartNewItemTimeout bounds the one read of the item.
const chartNewItemTimeout = 2 * time.Minute

// chartNewItem is `jig chart new <name> --item <ref>`: it makes the issue and
// its chart from an item that exists on the tracker and binds the two in one
// claim (mirror.BindItem). --title names the chart only; the issue's title is
// the item's. Every refusal happens before anything is minted.
func chartNewItem(e env, stdout io.Writer, name, itemRef, title, key string, needSet bool, issue string, jsonOut bool, projectFlag, storeFlag string) int {
	if needSet || issue != "" {
		return renderErr(stdout, &axi.Error{Msg: "--item excludes --need and --issue: the item is the issue's text, and the issue is made from it", Code: "VALIDATION_ERROR", Help: []string{chartNewUsage}})
	}
	ref, err := mirror.ParseItemRef(itemRef)
	if err != nil {
		return renderErr(stdout, err)
	}
	heading := strings.TrimSpace(title)
	if strings.ContainsAny(heading, "\r\n") {
		return renderErr(stdout, &axi.Error{Msg: "--title must be one line", Code: "VALIDATION_ERROR", Help: []string{"Put the chart's longer story in charts/<name>/map.md, under Destination"}})
	}
	if err := checkNewChartName(name); err != nil {
		return renderErr(stdout, err)
	}
	// The checkpoint sync reports to the stream the store was opened with: a
	// --json answer is one object, so the report goes nowhere then.
	var syncOut io.Writer = stdout
	if jsonOut {
		syncOut = io.Discard
	}
	st, cfg, _, jigHome, err := resolveStoreForProject(e, projectFlag, storeFlag, syncOut)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := mirror.CheckItemRepo(ref, cfg); err != nil {
		return renderErr(stdout, err)
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if err := mirror.CheckItemUnbound(st, cfg, ref); err != nil {
		return renderErr(stdout, err)
	}
	if err := chartNameTaken(st, name); err != nil {
		return renderErr(stdout, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), chartNewItemTimeout)
	defer cancel()
	res, err := mirror.BindItem(ctx, mirror.Deps{Store: st, Cfg: cfg, Home: jigHome, Client: e.mirrorClient}, mirror.BindOpts{
		Ref:        ref,
		Chart:      name,
		Heading:    heading,
		Key:        key,
		ChartTaken: func() error { return chartNameTaken(st, name) },
	})
	if err != nil {
		return renderErr(stdout, err)
	}

	// Claim never runs the checkpoint hook itself: this is the sync that finds
	// the issue's record and updates the item in place, opening no second issue.
	st.RunCheckpointHook()

	number := strconv.Itoa(res.Item.Number)
	folder := filepath.Join(st.Root, st.ChartRelDir(name))
	if jsonOut {
		data, err := json.Marshal(map[string]any{
			"chart":      map[string]string{"name": name, "title": res.Heading, "folder": folder},
			"issue":      res.Issue,
			"item":       map[string]any{"number": res.Item.Number, "url": res.Item.URL, "title": res.Item.Title, "open": res.Item.Open, "linked_pull_requests": res.Item.LinkedPullRequests},
			"advisories": append([]string{}, res.Advisories...),
		})
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	blocks := []string{axi.KV("chart", [][2]string{
		{"name", name}, {"title", res.Heading}, {"folder", folder},
		{"issue", res.Issue}, {"item", number}, {"item_url", res.Item.URL},
	})}
	if len(res.Advisories) > 0 {
		lines := make([]string, len(res.Advisories))
		for i, a := range res.Advisories {
			lines[i] = "  " + a
		}
		blocks = append(blocks, axi.Block{Header: fmt.Sprintf("advisories[%d]:", len(lines)), Lines: lines}.String())
	}
	blocks = append(blocks, axi.Help(
		fmt.Sprintf("Fill charts/%s/map.md and add the chart's tickets to charts/%s/tickets.yaml", name, name),
		fmt.Sprintf("Check them with `jig validate %s`: it lists every problem and mints nothing", name),
		fmt.Sprintf("Run `jig graduate %s` to create the tickets", name),
	))
	axi.Render(stdout, blocks...)
	return 0
}
