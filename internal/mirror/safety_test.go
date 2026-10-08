package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each sample below is built from separate string literals joined with +
// so the scanner sample itself (none of it is real data) does not read as
// a contiguous hit on the source line that defines it.
var (
	builtinTestNoreplyEmail = "1" + "+bot" + "@" + "users.noreply.github.com"
	builtinTestWindowsHome  = `C:\Users\` + `someone\notes.txt`
	builtinTestUnixHome     = `/home/` + `someone/notes.txt`
	builtinTestGitHubToken  = "ghp_" + "abcdef1234567890"
	builtinTestAnthropicKey = "sk-ant-" + "abc123"
	builtinTestAWSToken     = "AKIA" + "ABCDEFGHIJKLMNO"
	builtinTestKeyHeader    = "-----BEGIN RSA " + "PRIVATE" + " " + "KEY" + "-----"
)

// TestScanTitleAndBodyBuiltinPatterns checks each built-in pattern
// brief.md#Publish safety names, and its exemptions.
func TestScanTitleAndBodyBuiltinPatterns(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		body       string
		wantHit    bool
		wantLine   int
		wantSubstr string
	}{
		{"clean", "A clean title", "A clean body.\nNothing to see here.", false, 0, ""},
		{"email", "Title", "Reach me at " + publishSafetyTestEmail + " for details.", true, 2, publishSafetyTestEmail},
		{"noreply email exempt", "Title", "Committed as " + builtinTestNoreplyEmail, false, 0, ""},
		{"example.com exempt", "Title", "See jane@example.com for the fixture.", false, 0, ""},
		{"example.org exempt", "Title", "See jane@example.org for the fixture.", false, 0, ""},
		{"dot-invalid exempt", "Title", "See jane@host.invalid for the fixture.", false, 0, ""},
		{"windows home path", "Title", "Lives at " + builtinTestWindowsHome, true, 2, builtinTestWindowsHome},
		{"unix home path", "Title", "Lives at " + builtinTestUnixHome, true, 2, builtinTestUnixHome},
		{"github token", "Title", "token " + builtinTestGitHubToken, true, 2, builtinTestGitHubToken},
		{"anthropic token", "Title", "key " + builtinTestAnthropicKey, true, 2, builtinTestAnthropicKey},
		{"aws token", "Title", "key " + builtinTestAWSToken, true, 2, builtinTestAWSToken},
		{"private key header", "Title", builtinTestKeyHeader, true, 2, "PRIVATE" + " " + "KEY"},
		{"hit in title", "Contact " + publishSafetyTestEmail, "Clean body.", true, 1, publishSafetyTestEmail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits := ScanTitleAndBody(c.title, c.body, nil)
			if c.wantHit {
				if len(hits) == 0 {
					t.Fatalf("hits = none, want a hit naming %q", c.wantSubstr)
				}
				if hits[0].Line != c.wantLine {
					t.Fatalf("hits[0].Line = %d, want %d", hits[0].Line, c.wantLine)
				}
				if !strings.Contains(hits[0].Text, c.wantSubstr) {
					t.Fatalf("hits[0].Text = %q, want it to contain %q", hits[0].Text, c.wantSubstr)
				}
			} else if len(hits) != 0 {
				t.Fatalf("hits = %+v, want none", hits)
			}
		})
	}
}

// TestScanTitleAndBodyTerms checks that a term from publish-terms.txt hits
// as a case-insensitive substring, and a blank line or a "#" comment line in
// the file is never loaded as a term.
func TestScanTitleAndBodyTerms(t *testing.T) {
	hits := ScanTitleAndBody("Title", "The CodeName project ships Tuesday.", []string{"codename"})
	if len(hits) != 1 || hits[0].Line != 2 {
		t.Fatalf("hits = %+v, want one hit on line 2 (case-insensitive term match)", hits)
	}

	if hits := ScanTitleAndBody("Title", "codename is not mentioned", nil); len(hits) != 0 {
		t.Fatalf("hits = %+v, want none with no terms loaded", hits)
	}
}

// TestLoadPublishTerms checks LoadPublishTerms reads one term per line,
// lower-cased, skipping blank lines and "#" comments, and that a missing
// file (or an empty home) means no terms rather than an error.
func TestLoadPublishTerms(t *testing.T) {
	if terms, err := LoadPublishTerms(""); err != nil || terms != nil {
		t.Fatalf("LoadPublishTerms(\"\") = %v, %v, want nil, nil", terms, err)
	}

	home := t.TempDir()
	if terms, err := LoadPublishTerms(home); err != nil || terms != nil {
		t.Fatalf("LoadPublishTerms(no file) = %v, %v, want nil, nil", terms, err)
	}

	content := "# a comment\n\nCodeName\n  Acme Corp  \n"
	if err := os.WriteFile(filepath.Join(home, "publish-terms.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	terms, err := LoadPublishTerms(home)
	if err != nil {
		t.Fatalf("LoadPublishTerms: %v", err)
	}
	want := []string{"codename", "acme corp"}
	if len(terms) != len(want) {
		t.Fatalf("terms = %v, want %v", terms, want)
	}
	for i := range want {
		if terms[i] != want[i] {
			t.Fatalf("terms[%d] = %q, want %q", i, terms[i], want[i])
		}
	}
}
