package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishNamesEachReposFinalRun: in a ticket of two repos the builder of the
// api ran `jig verify --final` at its reviewed head and nobody did in the web.
// Publish's report and each pull request body then say it for each repo, by the
// repo each final-oracle line names: the api has a pass, the web has none, and
// each body says its own repo's.
func TestPublishNamesEachReposFinalRun(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	env, _ := githubOrchestrator(t, &b)
	ticket := b.fx.Ticket
	reviewedClean(t, b)

	verifySlots <- struct{}{}
	final := b.api.Jig("verify", ticket, "--final")
	<-verifySlots
	if final.Code != 0 || !strings.Contains(final.Stdout, "outcome: pass") {
		t.Fatalf("jig verify --final in the api: exit %d, want a pass\nstdout:\n%s\nstderr:\n%s", final.Code, final.Stdout, final.Stderr)
	}

	body := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--body")
	if body.Code != 0 {
		t.Fatalf("jig publish --body: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	for _, want := range []string{
		"final_run[2]{repo,outcome,seconds,command,run}:",
		"  api,pass,",
		"  web,none at the reviewed head " + b.shaWeb[:12] + ",,,jig verify " + ticket + " --final",
	} {
		if !strings.Contains(body.Stdout, want) {
			t.Errorf("jig publish --body lacks %q\n%s", want, body.Stdout)
		}
	}
	// Each repo's body speaks of its own reviewed head alone.
	for name, want := range map[string]string{
		"api.md": "Final run at the reviewed head " + b.shaAPI[:7] + ": pass in ",
		"web.md": "No final run at the reviewed head " + b.shaWeb[:7] + "\n",
	} {
		text := string(readFileOrFatal(t, filepath.Join(b.st.TicketDir(ticket), "pr", name)))
		if !strings.Contains(text, want) {
			t.Errorf("pr/%s lacks %q:\n%s", name, want, text)
		}
	}
}
