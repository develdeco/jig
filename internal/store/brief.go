package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// BriefSection is one "## " section of a brief: its heading, its body as the
// section hash covers it, and that hash.
type BriefSection struct {
	// Heading is the text after "## ", trimmed.
	Heading string
	// Body is the section from the line after the heading up to (but
	// excluding) the next "## " heading or EOF, with line endings normalized
	// to "\n" and trailing whitespace trimmed.
	Body string
	// Hash is the sha256 hex digest of Body: what a slice's from_brief holds.
	Hash string
}

// BriefSections returns the "## " sections of briefMD in the order they
// appear. Text before the first "## " heading belongs to no section.
func BriefSections(briefMD []byte) []BriefSection {
	text := strings.ReplaceAll(string(briefMD), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var out []BriefSection

	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "## ") {
			i++
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(lines[i], "## "))
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "## ") {
			j++
		}
		body := strings.TrimRight(strings.Join(lines[i+1:j], "\n"), " \t\n\r")
		sum := sha256.Sum256([]byte(body))
		out = append(out, BriefSection{Heading: heading, Body: body, Hash: hex.EncodeToString(sum[:])})
		i = j
	}
	return out
}

// BriefTitle returns the text of briefMD's first "# " heading, trimmed, or ""
// when it has none. A "## " heading is a section, never the title.
func BriefTitle(briefMD []byte) string {
	text := strings.ReplaceAll(string(briefMD), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// BriefSectionHashes returns, for every "## " heading in briefMD, the sha256
// hex digest of that section's body (BriefSections' Hash). A heading that
// appears twice keeps its later section's hash.
func BriefSectionHashes(briefMD []byte) map[string]string {
	out := map[string]string{}
	for _, sec := range BriefSections(briefMD) {
		out[sec.Heading] = sec.Hash
	}
	return out
}
