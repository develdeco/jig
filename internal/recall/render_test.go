package recall

import (
	"strings"
	"testing"
)

func TestBlocksRenderEverySectionTheReportHas(t *testing.T) {
	t.Parallel()
	m := Material{
		Charts: []Chart{{Name: "uploads", Map: "## Not yet specified (fog)\n\n- Does a cancelled upload resume?\n"}},
		Docs:   []DocText{{Path: "platform/errors.md", Bytes: 40, Text: "# Error codes\n\nA retry follows a NETWORK code.\n"}},
		Ledger: "## JIG-7 - Add retry\n\n**Answers:**\n- q-001: How often? -> Three times, with backoff.\n",
	}
	r := Build(sampleTickets(), m, "retry fetch client backoff")
	out := strings.Join(r.Blocks(), "\n")
	for _, want := range []string{
		"recall:\n  query: retry fetch client backoff\n  open_tickets: 3\n  shipped_tickets: 1\n",
		"tickets[3]{id,state,title}:\n  JIG-2,ready,Add retry with backoff to the fetch client\n",
		"edges[3]{ticket,blocked_by,kind,blocker}:\n  JIG-3,JIG-1,merged,shipped\n  JIG-3,JIG-2,stacked,waiting\n  JIG-3,JIG-99,merged,dangling\n",
		"duplicates[2]{ticket,score,in,state,title}:\n  JIG-4,1.00,title,active,Retry fetch client with backoff\n  JIG-2,0.80,title,ready,",
		"fog[1]{chart,item}:\n  uploads,Does a cancelled upload resume?\n",
		"platform[1]{path,title,bytes}:\n  platform/errors.md,Error codes,40\n",
		"platform_lines[2]{path,kind,line}:\n  platform/errors.md,head,# Error codes\n",
		"answers[1]{ticket,title,id,text}:\n  JIG-7,Add retry,q-001,\"How often? -> Three times, with backoff.\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("blocks lack %q:\n%s", want, out)
		}
	}
}

func TestBlocksLeaveOutWhatIsEmptyAndSayWhenAQueryFoundNothing(t *testing.T) {
	t.Parallel()
	out := strings.Join(Build(nil, Material{}, "").Blocks(), "\n")
	if out != "recall:\n  open_tickets: 0\n  shipped_tickets: 0" {
		t.Errorf("an empty report renders as %q", out)
	}
	out = strings.Join(Build(sampleTickets(), Material{}, "quantum teleportation").Blocks(), "\n")
	if !strings.Contains(out, "\nduplicates: none") || strings.Contains(out, "edges[0]") {
		t.Errorf("a query that found nothing renders as:\n%s", out)
	}
}
