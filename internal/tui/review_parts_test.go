package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestReviewSectionsArePanels: the review of a ticket with two rounds, findings,
// probes, a fix slice sent back and an ask draws each as a panel with a
// capitalized title, every list under the names of its columns and no row the
// cursor stands on outside a panel; at three sizes.
func TestReviewSectionsArePanels(t *testing.T) {
	t.Parallel()
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		for _, rows := range []int{h, 100} {
			g := newRig(t, "JIG-2")
			g.r.answer("status JIG-2 --store /s", probeStatus([][]string{
				probeRound("1", "3", "1"),
				probeRound("2", "2", "0"),
			},
				axi.Table("probes", probeColumns, [][]string{
					{"1", "caught", "alpha/a.go:9", "the parser", "flip the guard", "go test ./alpha"},
					{"1", "survived", "beta/b.go:4", "the greeting", "drop the check", "go test ./beta"},
					{"2", "caught", "beta/b.go:4", "the greeting", "drop the check", "go test ./beta"},
				}),
				axi.Table("findings", findingColumns, [][]string{
					{"r1-f2", "1", "fix", "medium", "alpha/a.go:12", "Close the file", "open", "a leaked handle fails the next open", "", ""},
				}),
			), 0)
			g.feed(append(jig2Mid(),
				jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
				jl(at(12, 40), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
				jl(at(12, 41), "JIG-2", "fix-1", journal.EventFixSlice),
			)...)
			g.send(tea.WindowSizeMsg{Width: w, Height: rows})
			g.press("enter")
			var names map[string]string
			if rows != h {
				names = map[string]string{"Rounds": "round", "Probes": "round", "Asks": "risk", "Review activity": "time"}
			}
			wantPanels(t, fmt.Sprintf("the review %dx%d", w, rows), g.view(), names)
		}
	}
}
