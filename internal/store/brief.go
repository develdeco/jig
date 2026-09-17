package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// BriefSectionHashes returns, for every "## " heading in briefMD, the sha256
// hex digest of that section's body: from the line after the heading up to
// (but excluding) the next "## " heading or EOF, with line endings
// normalized to "\n" and trailing whitespace trimmed.
func BriefSectionHashes(briefMD []byte) map[string]string {
	text := strings.ReplaceAll(string(briefMD), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := map[string]string{}

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
		out[heading] = hex.EncodeToString(sum[:])
		i = j
	}
	return out
}
