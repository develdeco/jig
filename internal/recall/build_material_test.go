package recall

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCarriesShortAnswersNewestFirst(t *testing.T) {
	t.Parallel()
	var ledger strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&ledger, "\n## JIG-%d - Entry %d\n\n**Answers:**\n- q-001: Question %d? -> %s\n", i, i, i, strings.Repeat("long ", 100))
	}
	ledger.WriteString("\n## JIG-13 - No answers\n\n**Answers:**\nnone\n")
	r := Build(nil, Material{Ledger: ledger.String()}, "")
	if len(r.Answers) != MaxAnswerEntries || r.Answers[0].Ticket != "JIG-12" || r.Answers[MaxAnswerEntries-1].Ticket != "JIG-3" {
		t.Fatalf("answers = %+v, want the latest %d entries with answers, JIG-12 first", r.Answers, MaxAnswerEntries)
	}
	if n := len([]rune(r.Answers[0].Answers[0].Text)); n != MaxItemRunes {
		t.Errorf("an answer is %d characters, want it cut to %d", n, MaxItemRunes)
	}
}

func TestBuildRanksAnswersByTheWordsTheyShareWithTheQuery(t *testing.T) {
	t.Parallel()
	ledger := "## JIG-1 - Upload retry\n\n**Answers:**\n- q-001: Retry how often? -> Three times with backoff.\n" +
		"\n## JIG-2 - Upload retry backoff\n\n**Answers:**\n- q-001: Which one? -> Exponential.\n" +
		"\n## JIG-3 - Greeting\n\n**Answers:**\n- q-001: Tone? -> Casual.\n" +
		"\n## JIG-4 - Upload retry backoff again\n\n**Answers:**\n- q-001: Cap? -> Ten.\n"
	r := Build(nil, Material{Ledger: ledger}, "upload retry backoff")
	var ids []string
	for _, e := range r.Answers {
		ids = append(ids, e.Ticket)
	}
	// JIG-4 and JIG-2 share three words, JIG-1 shares three as well through
	// its answer, so the newest of the three leads; JIG-3 shares none.
	if want := []string{"JIG-4", "JIG-2", "JIG-1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("answers = %q, want %q", ids, want)
	}
}

func TestBuildTakesFogAndPlatformWithTheirCaps(t *testing.T) {
	t.Parallel()
	var fog strings.Builder
	fog.WriteString("## Not yet specified (fog)\n\n")
	for i := 0; i < MaxFogItems+5; i++ {
		fmt.Fprintf(&fog, "- item %d\n", i)
	}
	m := Material{Charts: []Chart{{Name: "a", Map: fog.String()}, {Name: "b", Map: "# no fog here\n"}}}
	for i := 0; i < MaxDocs+3; i++ {
		m.Docs = append(m.Docs, DocText{Path: fmt.Sprintf("platform/%02d.md", i), Text: "# Doc\n"})
	}
	r := Build(nil, m, "")
	if len(r.Fog) != 1 || r.Fog[0].Chart != "a" || len(r.Fog[0].Items) != MaxFogItems || r.Fog[0].More != 5 {
		t.Errorf("fog = %+v, want chart a with %d items and 5 more, and no entry for the chart without fog", r.Fog, MaxFogItems)
	}
	if len(r.Platform) != MaxDocs {
		t.Errorf("platform documents = %d, want %d", len(r.Platform), MaxDocs)
	}
}
