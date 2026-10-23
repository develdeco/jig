package recall

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/store"
)

// Chart is a chart's map.md as Read took it.
type Chart struct {
	Name string
	Map  string
}

// Material is what a report draws from the store's files besides its tickets:
// the charts' maps, the platform documents and the ledger.
type Material struct {
	Charts []Chart
	Docs   []DocText
	Ledger string
}

// Read takes the Material out of the store at st: every charts/<name>/map.md
// (a chart with no map has none to give), the markdown documents below
// platform (the project config's platform directory; "" means "platform/")
// and ledger.md. An absent file or directory is empty material, not an error.
func Read(st *store.Store, platform string) (Material, error) {
	var m Material
	entries, err := os.ReadDir(filepath.Join(st.Root, "charts"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Material{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(st.ChartMapFile(e.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Material{}, err
		}
		m.Charts = append(m.Charts, Chart{Name: e.Name(), Map: string(data)})
	}
	sort.Slice(m.Charts, func(i, j int) bool { return m.Charts[i].Name < m.Charts[j].Name })
	if m.Docs, err = readDocs(st.Root, platform); err != nil {
		return Material{}, err
	}
	data, err := os.ReadFile(filepath.Join(st.Root, "ledger.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Material{}, err
	}
	m.Ledger = string(data)
	return m, nil
}
