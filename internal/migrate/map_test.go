package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMapFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMapParsesKeysAndTickets(t *testing.T) {
	t.Parallel()
	path := writeMapFile(t, `
keys:
  STORE: the store's layout, ids, records and git sync
  GRAPH: tickets, charts and the order between them
tickets:
  T-1: GRAPH
  T-23: STORE
`)
	m, err := LoadMap(path)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	if m.Keys["STORE"] == "" || m.Keys["GRAPH"] == "" {
		t.Errorf("Keys = %+v, want both STORE and GRAPH", m.Keys)
	}
	if m.Tickets["T-1"] != "GRAPH" || m.Tickets["T-23"] != "STORE" {
		t.Errorf("Tickets = %+v, want T-1:GRAPH and T-23:STORE", m.Tickets)
	}
}

func TestLoadMapRefusesBadKey(t *testing.T) {
	t.Parallel()
	path := writeMapFile(t, "keys:\n  store: lowercase is refused\ntickets: {}\n")
	_, err := LoadMap(path)
	wantValidationError(t, err)
}

func TestLoadMapRefusesEmptyMeaning(t *testing.T) {
	t.Parallel()
	path := writeMapFile(t, "keys:\n  STORE: \"\"\ntickets: {}\n")
	_, err := LoadMap(path)
	wantValidationError(t, err)
}

func TestLoadMapRefusesMissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadMap(filepath.Join(t.TempDir(), "absent.yaml"))
	wantValidationError(t, err)
}
