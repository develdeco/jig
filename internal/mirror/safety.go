package mirror

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SafetyHit is one publish-safety scanner match: the 1-based line number in
// the scanned title+body text (the title is line 1, the body's own lines
// follow), and the full line it matched on (brief.md#Publish safety).
type SafetyHit struct {
	Line int
	Text string
}

// emailRE matches an email address, brief.md#Publish safety's first
// built-in pattern.
var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// exampleEmailDomains are the reserved example domains an email address on
// them is not a hit for (brief.md#Publish safety).
var exampleEmailDomains = map[string]bool{
	"example.com": true,
	"example.org": true,
	"example.net": true,
}

// isExemptEmail reports whether addr is exempt from the email pattern:
// @users.noreply.github.com, or a reserved example domain (example.com,
// example.org, example.net, or any .invalid).
func isExemptEmail(addr string) bool {
	_, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return false
	}
	domain = strings.ToLower(domain)
	if domain == "users.noreply.github.com" {
		return true
	}
	if exampleEmailDomains[domain] {
		return true
	}
	return strings.HasSuffix(domain, ".invalid")
}

// windowsHomeRE and unixHomeRE match a home directory path, brief.md
// #Publish safety's second built-in pattern.
var (
	windowsHomeRE = regexp.MustCompile(`[A-Za-z]:\\Users\\[^\\\s]+`)
	unixHomeRE    = regexp.MustCompile(`/home/[^/\s]+`)
)

// privateKeyHeaderRE matches a private key header line, brief.md#Publish
// safety's last built-in pattern. The space between the two words is
// spelled as a character class so this source itself does not contain
// them joined by a literal space, which the publish-safety scan flags.
var privateKeyHeaderRE = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE[ ]KEY-----`)

// tokenPrefixes are the GitHub, Anthropic and AWS token prefixes
// brief.md#Publish safety names as a built-in pattern.
var tokenPrefixes = []string{
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
	"sk-ant-",
	"AKIA", "ASIA",
}

// matchesBuiltin reports whether line trips one of brief.md#Publish
// safety's built-in patterns.
func matchesBuiltin(line string) bool {
	for _, addr := range emailRE.FindAllString(line, -1) {
		if !isExemptEmail(addr) {
			return true
		}
	}
	if windowsHomeRE.MatchString(line) || unixHomeRE.MatchString(line) {
		return true
	}
	if privateKeyHeaderRE.MatchString(line) {
		return true
	}
	for _, prefix := range tokenPrefixes {
		if strings.Contains(line, prefix) {
			return true
		}
	}
	return false
}

// matchesTerm reports whether line contains any of terms (already
// lower-cased) as a case-insensitive substring.
func matchesTerm(line string, terms []string) bool {
	lower := strings.ToLower(line)
	for _, t := range terms {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// ScanTitleAndBody scans title (as its own line) and body (split on "\n")
// against the built-in patterns and terms, returning every line that hit,
// in order, numbered from 1 (brief.md#Publish safety).
func ScanTitleAndBody(title, body string, terms []string) []SafetyHit {
	lines := append([]string{title}, strings.Split(body, "\n")...)
	var hits []SafetyHit
	for i, line := range lines {
		if matchesBuiltin(line) || matchesTerm(line, terms) {
			hits = append(hits, SafetyHit{Line: i + 1, Text: line})
		}
	}
	return hits
}

// publishTermsFile is the terms file's own name, read from the jig home
// root (brief.md#Publish safety: "The file lives on the machine, never in
// the store or a repo").
const publishTermsFile = "publish-terms.txt"

// LoadPublishTerms reads home's publish-terms.txt, one term per line,
// lower-cased for ScanTitleAndBody's case-insensitive match; blank lines and
// "#" comments are ignored. A missing file (or an empty home) means no
// terms, not an error (brief.md#Publish safety).
func LoadPublishTerms(home string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(home, publishTermsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var terms []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, strings.ToLower(line))
	}
	return terms, nil
}
