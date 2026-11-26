package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
)

// fixRoundSlices are the slices of a ticket in its fix round: four built as the
// build screen's records tell, and a fix slice that came from round 2 and answers
// the finding r1-f1.
func fixRoundSlices() string {
	goal := "Fix these gate findings:\n\npublish.go:1105 The stacked parent's fallback is no longer reached\nA stale parent would merge.\nRisk (high): the fallback is the only guard."
	return axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
		{"a", "green", "1", "-", "-", "Greet by name", "app", "unit", "-", "-"},
		{"b", "building", "2", "-", "-", "Say goodbye, politely", "app", "go test ./...", "-", "-"},
		{"c", "needs-input", "1", "-", "q-001", "Pick the tone", "copy", "go test ./...", "-", "-"},
		{"d", "queued", "0", "b+c", "-", "Ship the copy", "copy", "go test ./...", "-", "-"},
		{"fix-2-app", "queued", "0", "-", "-", goal, "app", "go test ./...", "2", "r1-f1"},
	})
}

// fixRoundStatus is `jig status JIG-1` for jig1Records as a ticket in its fix
// round: five slices, one of them a fix slice.
func fixRoundStatus() string {
	return strings.Join([]string{
		"ticket: JIG-1",
		"state: parked",
		fixRoundSlices(),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "an agent (as Ada Lovelace)"}, {"identity", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "agent"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{
			{"Goal", "-"}, {"Greeting", "a"}, {"Farewell", "b"}, {"Tone", "c+d"},
		}),
		axi.Help("Answer q-001: `jig answer JIG-1 q-001 '<text>'`"),
	}, "\n") + "\n"
}

// sized is the rig's screen at w x h.
func (g *rig) sized(w, h int) *rig {
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	return g
}
