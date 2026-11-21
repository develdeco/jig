package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSuitePublishBodyCarriesTheVerificationLines: the lines the standing hands
// in become the Verification section of an issue's pull request body, after the
// children and before the trailer; a standing with none leaves no section.
func TestSuitePublishBodyCarriesTheVerificationLines(t *testing.T) {
	t.Parallel()
	d, standing, _ := containerDeps(t)
	for _, l := range []journal.Line{{Event: journal.EventPr, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}, {Event: journal.EventLanded, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}} {
		if err := journal.Append(d.Store, "M-1", l); err != nil {
			t.Fatal(err)
		}
	}
	d.Cfg.PublishTrailer = "Opened by an agent"
	standing.Verification = []string{"Suite waived at abc123: ci runs it"}
	useGithubHost(t, d, "", "")

	report, err := PublishContainer(*d, PublishOpts{Ticket: "I-1", Yes: true})
	if err != nil {
		t.Fatalf("PublishContainer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	i, j, k := strings.Index(body, "## Children"), strings.Index(body, "## Verification\n\n- Suite waived at abc123: ci runs it\n"), strings.Index(body, "Opened by an agent")
	if i < 0 || j < i || k < j {
		t.Errorf("the body does not order children, verification and trailer:\n%s", body)
	}
}
