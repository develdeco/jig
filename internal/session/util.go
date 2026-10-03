package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// respellMentions returns prompt with every mention of one of paths replaced
// by spell(path): how a backend tells a session about a path in the spelling
// the session itself uses, where that differs from jig's (headless: the long
// spelling of a Windows 8.3 short name; herdr on Windows: the WSL mount). The
// longest path goes first, so a path that contains another one is replaced
// whole. A path that is empty, or that spell leaves as it is, is skipped.
func respellMentions(prompt string, paths []string, spell func(string) string) string {
	ordered := append([]string(nil), paths...)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, p := range ordered {
		if p == "" {
			continue
		}
		if spelled := spell(p); spelled != p {
			prompt = strings.ReplaceAll(prompt, p, spelled)
		}
	}
	return prompt
}

// writeResultBytes writes data to path, creating the parent directory if
// needed (the destination may be inside a lease dir jig hasn't populated
// yet).
func writeResultBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeJSONResult marshals v and writes it to path via writeResultBytes.
func writeJSONResult(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeResultBytes(path, data)
}
