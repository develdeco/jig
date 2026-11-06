package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// bodyHeadings returns the "## " headings of a rendered pull request body, in
// order.
func bodyHeadings(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, line)
		}
	}
	return out
}

// TestPublishBodyShapeIsWhatAndVerification: the body of a ticket with no
// demo is the CONTRIBUTING shape and nothing else, a ## What whose one bullet
// is the ticket's title until the session writes its own, and the ## Verification.
func TestPublishBodyShapeIsWhatAndVerification(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	if err := d.Store.CreateTicketRecord(fx.Ticket, store.Ticket{Title: "Recorded title"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	gateToClean(t, fx, d)

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil {
		t.Fatalf("read pr body: %v", err)
	}
	body := string(data)

	want := []string{"## What", "## Verification"}
	if got := bodyHeadings(body); !reflect.DeepEqual(got, want) {
		t.Fatalf("pr body sections = %v, want %v:\n%s", got, want, body)
	}
	if !strings.Contains(body, "## What\n\n- Recorded title\n") {
		t.Fatalf("pr body ## What is not the ticket's title as one bullet:\n%s", body)
	}
}
