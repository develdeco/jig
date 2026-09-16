package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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
