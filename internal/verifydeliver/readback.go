package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// Readback is what `jig publish --pr` found when it read a pull request's
// title and body back from the host and held them against the render.
type Readback struct {
	// Match is journal.BodyMatches, journal.BodyDiffers or journal.BodyUnknown.
	Match string
	// Differs says what differs, "title", "body" or "title and body", when
	// Match is journal.BodyDiffers.
	Differs string
}

// readBack reads the title and body of the pull request at prURL from the host
// and says whether they are the ones the render holds: title is the title the
// render reports, and the body is the store's pr/<repoName>.md. The ## Demo
// section is left out of both bodies, since the host rewrites the recordings
// it shows into uploads of its own, so a pull request that shows them never
// reads as its render. A body that cannot be compared is unknown, not a
// failure: there is no host to ask, the host failed, or no body was rendered
// yet. The pull request is recorded either way.
func readBack(host repohost.Host, st *store.Store, ticket, repoName, title, prURL string, warn func(format string, args ...any)) Readback {
	if host == nil {
		return Readback{Match: journal.BodyUnknown}
	}
	rendered, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "pr", repoName+".md"))
	if err != nil {
		return Readback{Match: journal.BodyUnknown}
	}
	gotBody, err := host.ReadPRBody(prURL)
	if err != nil {
		warn("jig: read the body of %s back: %v\n", prURL, err)
		return Readback{Match: journal.BodyUnknown}
	}
	gotTitle, err := host.ReadPRTitle(prURL)
	if err != nil {
		warn("jig: read the title of %s back: %v\n", prURL, err)
		return Readback{Match: journal.BodyUnknown}
	}
	var differs []string
	if normalizeText(gotTitle) != normalizeText(title) {
		differs = append(differs, "title")
	}
	if normalizeText(withoutDemo(gotBody)) != normalizeText(withoutDemo(string(rendered))) {
		differs = append(differs, "body")
	}
	if len(differs) == 0 {
		return Readback{Match: journal.BodyMatches}
	}
	return Readback{Match: journal.BodyDiffers, Differs: strings.Join(differs, " and ")}
}

// normalizeText is text as the comparison reads it: LF line endings, which a
// body edited in the host's web UI comes back without, and no whitespace
// around it.
func normalizeText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// withoutDemo is body without the text of its ## Demo section.
func withoutDemo(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	start, end, ok := demoSectionBounds(body)
	if !ok {
		return body
	}
	return body[:start] + body[end:]
}
