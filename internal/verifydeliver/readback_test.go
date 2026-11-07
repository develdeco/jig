package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// lastPrLine is the latest pr line of the ticket's journal.
func lastPrLine(t *testing.T, d Deps, ticket string) journal.Line {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var pr journal.Line
	for _, l := range lines {
		if l.Event == journal.EventPr {
			pr = l
		}
	}
	if pr.Event == "" {
		t.Fatal("the journal has no pr line")
	}
	return pr
}

// rawPrLine is the text of the latest pr line in the ticket's journal file,
// as written: the keys a journal outlives its builds by.
func rawPrLine(t *testing.T, d Deps, ticket string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(d.Store.TicketDir(ticket), "journal.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var pr string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"event":"`+journal.EventPr+`"`) {
			pr = line
		}
	}
	if pr == "" {
		t.Fatal("the journal file has no pr line")
	}
	return pr
}

// TestPublishRecordPRReadsTheTitleAndBodyBack: --pr reads the pull request's
// title and body back from the host and journals on its pr line whether they
// are the render's: matches when they are (whatever the line endings the host
// serves), differs when the title or the body is not, and says which in a
// warning.
func TestPublishRecordPRReadsTheTitleAndBodyBack(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"

	cases := []struct {
		name        string
		body        func(render string) string
		title       func(title string) string
		want        string
		wantDiffers string
	}{
		{"the render itself", func(r string) string { return r }, func(s string) string { return s }, journal.BodyMatches, ""},
		{"the render served with CRLF endings", func(r string) string { return strings.ReplaceAll(r, "\n", "\r\n") }, func(s string) string { return s }, journal.BodyMatches, ""},
		{"another title", func(r string) string { return r }, func(string) string { return "Another title" }, journal.BodyDiffers, "title"},
		{"another body", func(r string) string { return r + "\n## Intent\n\nAppended history.\n" }, func(s string) string { return s }, journal.BodyDiffers, "body"},
		{"another title and body", func(string) string { return "## Intent\n\nOld.\n" }, func(string) string { return "Old" }, journal.BodyDiffers, "title and body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := reviewedInTheClone(t)
			_, addEnv := useGithubHost(t, &d, "", "")
			rendered, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("PublishBody: %v", err)
			}
			render, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(rendered.Repos[0].PRBody)))
			if err != nil {
				t.Fatal(err)
			}
			addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head,
				"GH_STUB_BODY="+tc.body(string(render)), "GH_STUB_TITLE="+tc.title(rendered.Repos[0].Title))

			var warnings []string
			report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(format string, args ...any) {
				warnings = append(warnings, fmt.Sprintf(format, args...))
			})
			if err != nil {
				t.Fatalf("PublishRecordPR: %v", err)
			}
			if report.Readback.Match != tc.want || report.Readback.Differs != tc.wantDiffers {
				t.Errorf("readback = %+v, want %s (%s)", report.Readback, tc.want, tc.wantDiffers)
			}
			if got := lastPrLine(t, d, fx.Ticket).BodyMatch; got != tc.want {
				t.Errorf("the pr line says body_match %q, want %q", got, tc.want)
			}
			if raw := rawPrLine(t, d, fx.Ticket); !strings.Contains(raw, `"body_match":"`+tc.want+`"`) {
				t.Errorf("the pr line's journal text = %s, want it to carry body_match %q", raw, tc.want)
			}
			warned := false
			for _, w := range warnings {
				warned = warned || strings.Contains(w, "differs from the render")
			}
			if warned != (tc.want == journal.BodyDiffers) {
				t.Errorf("warnings = %q, want one about the render only when it differs", warnings)
			}
			if tc.wantDiffers != "" {
				for _, w := range warnings {
					if strings.Contains(w, "differs from the render") && !strings.Contains(w, "the "+tc.wantDiffers+" of") {
						t.Errorf("warning = %q, want it to name the %s", w, tc.wantDiffers)
					}
				}
			}
		})
	}
}

// TestPublishRecordPRCannotReadBackWithoutAHostOrABody: with no host to ask,
// or a failed read, the pr line says unknown and the pull request is recorded
// all the same; a body not rendered yet is rendered first, and held against.
func TestPublishRecordPRCannotReadBackWithoutAHostOrABody(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"

	t.Run("no host", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTheClone(t)
		if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("PublishBody: %v", err)
		}
		report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(string, ...any) {})
		if err != nil {
			t.Fatalf("PublishRecordPR: %v", err)
		}
		if report.Verified || report.Readback.Match != journal.BodyUnknown {
			t.Errorf("report = %+v, want a pull request recorded unverified with the readback unknown", report)
		}
		if got := lastPrLine(t, d, fx.Ticket).BodyMatch; got != journal.BodyUnknown {
			t.Errorf("the pr line says body_match %q, want %q", got, journal.BodyUnknown)
		}
	})

	// A session that never ran --body and opened the pull request with its own
	// body: --pr renders the body for the reviewed head first and holds the
	// pull request against that.
	t.Run("no rendered body", func(t *testing.T) {
		t.Parallel()
		fx, d, head := reviewedInTheClone(t)
		_, addEnv := useGithubHost(t, &d, "", "")
		addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
		report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(string, ...any) {})
		if err != nil {
			t.Fatalf("PublishRecordPR: %v", err)
		}
		if report.Readback.Match != journal.BodyDiffers {
			t.Errorf("readback = %+v, want differs: the host's canned body is not the render --pr just made", report.Readback)
		}
		if got := lastPrLine(t, d, fx.Ticket).BodyMatch; got != journal.BodyDiffers {
			t.Errorf("the pr line says body_match %q, want %q", got, journal.BodyDiffers)
		}
	})

	// A body last rendered for an earlier head: --pr refreshes it, so a pull
	// request opened from the old render differs, and one carrying the
	// refreshed render matches.
	for _, tc := range []struct {
		name  string
		serve func(stale, fresh string) string
		want  string
	}{
		{"a pull request opened from the earlier render", func(stale, _ string) string { return stale }, journal.BodyDiffers},
		{"a pull request carrying the render for the reviewed head", func(_, fresh string) string { return fresh }, journal.BodyMatches},
	} {
		t.Run("render of an earlier head, "+tc.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := reviewedInTheClone(t)
			_, addEnv := useGithubHost(t, &d, "", "")
			rendered, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("PublishBody: %v", err)
			}
			path := filepath.Join(d.Store.Root, filepath.FromSlash(rendered.Repos[0].PRBody))
			fresh, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			const stale = "## What\n\n- The body as an earlier round rendered it.\n"
			if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventPrBody, Commit: "0000000000000000000000000000000000000000"}); err != nil {
				t.Fatal(err)
			}
			addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head,
				"GH_STUB_BODY="+tc.serve(stale, string(fresh)), "GH_STUB_TITLE="+rendered.Repos[0].Title)
			report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(string, ...any) {})
			if err != nil {
				t.Fatalf("PublishRecordPR: %v", err)
			}
			if report.Readback.Match != tc.want {
				t.Errorf("readback = %+v, want %s", report.Readback, tc.want)
			}
			if got := lastPrLine(t, d, fx.Ticket).BodyMatch; got != tc.want {
				t.Errorf("the pr line says body_match %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPublishRecordPRIgnoresWhatTheHostMadeOfTheDemo: the render shows a
// recording by the path of the file and the host rewrites it into an upload of
// its own, so the ## Demo section is left out of the comparison and the pull
// request still reads as the render.
func TestPublishRecordPRIgnoresWhatTheHostMadeOfTheDemo(t *testing.T) {
	t.Parallel()
	const prURL = "https://github.com/owner/fixture-repo/pull/7"
	fx, d, head := reviewedInTheClone(t)
	_, addEnv := useGithubHost(t, &d, "", "")
	rendered, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	path := filepath.Join(d.Store.Root, filepath.FromSlash(rendered.Repos[0].PRBody))
	plain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	demo := func(shown string) string {
		return strings.Replace(string(plain), "## Verification", "## Demo\n\n- ![it works]("+shown+")\n\n## Verification", 1)
	}
	if err := os.WriteFile(path, []byte(demo("./rec-1.png")), 0o644); err != nil {
		t.Fatal(err)
	}
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head,
		"GH_STUB_BODY="+demo("https://github.example/user-attachments/assets/1"), "GH_STUB_TITLE="+rendered.Repos[0].Title)

	report, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, prURL, func(string, ...any) {})
	if err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	if report.Readback.Match != journal.BodyMatches {
		t.Errorf("readback = %+v, want matches: only the Demo differs, and the host made it", report.Readback)
	}
}
