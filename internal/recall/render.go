package recall

import (
	"fmt"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
)

// Blocks renders the report in the jig text register, the sections that have
// nothing left out. The help line is the caller's, since the next step
// depends on the verb that asked.
func (r Report) Blocks() []string {
	head := [][2]string{}
	if r.Query != "" {
		head = append(head, [2]string{"query", r.Query})
	}
	head = append(head, [2]string{"open_tickets", strconv.Itoa(len(r.Tickets))}, [2]string{"shipped_tickets", strconv.Itoa(r.Shipped)})
	blocks := []string{axi.KV("recall", head)}

	if len(r.Tickets) > 0 {
		rows := make([][]string, len(r.Tickets))
		for i, t := range r.Tickets {
			rows[i] = []string{t.ID, t.State, t.Title}
		}
		blocks = append(blocks, axi.Table("tickets", []string{"id", "state", "title"}, rows))
	}
	if len(r.Edges) > 0 {
		rows := make([][]string, len(r.Edges))
		for i, e := range r.Edges {
			status := "waiting"
			switch {
			case e.Dangling:
				status = "dangling"
			case e.BlockerShipped:
				status = "shipped"
			}
			rows[i] = []string{e.Ticket, e.BlockedBy, e.Kind, status}
		}
		blocks = append(blocks, axi.Table("edges", []string{"ticket", "blocked_by", "kind", "blocker"}, rows))
	}
	if r.Query != "" {
		if len(r.Duplicates) == 0 {
			blocks = append(blocks, "duplicates: none")
		} else {
			rows := make([][]string, len(r.Duplicates))
			for i, d := range r.Duplicates {
				rows[i] = []string{d.Ticket, fmt.Sprintf("%.2f", d.Score), d.In, d.State, d.Title}
			}
			blocks = append(blocks, axi.Table("duplicates", []string{"ticket", "score", "in", "state", "title"}, rows))
		}
	}
	if len(r.Fog) > 0 {
		var rows [][]string
		for _, f := range r.Fog {
			for _, it := range f.Items {
				rows = append(rows, []string{f.Chart, it})
			}
			if f.More > 0 {
				rows = append(rows, []string{f.Chart, fmt.Sprintf("(%d more in the map)", f.More)})
			}
		}
		blocks = append(blocks, axi.Table("fog", []string{"chart", "item"}, rows))
	}
	if len(r.Platform) > 0 {
		docs := make([][]string, len(r.Platform))
		var lines [][]string
		for i, d := range r.Platform {
			docs[i] = []string{d.Path, d.Title, strconv.Itoa(d.Bytes)}
			for _, l := range d.Head {
				lines = append(lines, []string{d.Path, "head", l})
			}
			for _, l := range d.Matches {
				lines = append(lines, []string{d.Path, "match", l})
			}
		}
		blocks = append(blocks, axi.Table("platform", []string{"path", "title", "bytes"}, docs))
		if len(lines) > 0 {
			blocks = append(blocks, axi.Table("platform_lines", []string{"path", "kind", "line"}, lines))
		}
	}
	if len(r.Answers) > 0 {
		var rows [][]string
		for _, e := range r.Answers {
			for _, a := range e.Answers {
				rows = append(rows, []string{e.Ticket, e.Title, a.ID, a.Text})
			}
		}
		blocks = append(blocks, axi.Table("answers", []string{"ticket", "title", "id", "text"}, rows))
	}
	return blocks
}
