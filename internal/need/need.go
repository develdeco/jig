// Package need keeps what a project's heavy oracle needs of the machine, as
// measured on this machine from its own recent runs: the cores it kept busy
// on average (CPU seconds over wall seconds) and the peak memory its process
// tree held. internal/slot admits a run by it.
//
// The figure is per project and oracle name, per machine, and lives under the
// jig home, never in the store: it describes this machine. It is the largest
// of the last few runs of that oracle, taking the largest cores and the
// largest peak memory among them, so that a few small selections (a test run
// narrowed to some packages) do not hide the whole suite under the same
// oracle name. A record from another count of CPUs or another amount of
// memory, or too old, is not counted.
package need

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/machine"
)

const (
	// Keep is how many of an oracle's latest runs make its figure.
	Keep = 5
	// MaxAge is how long a run counts.
	MaxAge = 30 * 24 * time.Hour
)

// Need is what an oracle needs of the machine.
type Need struct {
	// Cores is the cores it kept busy on average.
	Cores float64
	// PeakBytes is its peak memory, 0 when no run measured it (memory is then
	// not budgeted for it).
	PeakBytes uint64
	// Basis is how PeakBytes was measured: the basis of the run it came from.
	Basis string
}

// record is one run as kept.
type record struct {
	At        time.Time `json:"at"`
	CPUs      int       `json:"cpus"`
	MemBytes  uint64    `json:"mem_bytes"`
	Cores     float64   `json:"cores"`
	PeakBytes uint64    `json:"peak_bytes,omitempty"`
	Basis     string    `json:"peak_basis,omitempty"`
}

// now is the clock, replaced by tests.
var now = time.Now

// path is the file of a project's oracle: the oracle's name goes through a
// hash since a name may hold characters a file name cannot.
func path(root, project, oracle string) string {
	sum := sha256.Sum256([]byte(oracle))
	return filepath.Join(root, "needs", project, hex.EncodeToString(sum[:8])+".json")
}

func (r record) counts(m machine.Reading, at time.Time) bool {
	return r.CPUs == m.CPUs && r.MemBytes == m.MemoryBytes && at.Sub(r.At) <= MaxAge
}

func load(p string) []record {
	raw, err := fileshare.ReadFile(p)
	if err != nil {
		return nil
	}
	var rs []record
	if json.Unmarshal(raw, &rs) != nil {
		return nil
	}
	return rs
}

// Record keeps a heavy run that ended on its own (a verdict, pass or fail; a
// run jig stopped is not recorded, since it did not finish using what it
// would): its wall time and what its process tree used. A run whose CPU time
// was not measured is not recorded. It is best effort: a figure that cannot be
// written leaves the next run unmeasured, which asks for the whole machine.
func Record(root, project, oracle string, m machine.Reading, wall time.Duration, use envrun.Usage) {
	if project == "" || oracle == "" || wall <= 0 || use.CPU <= 0 {
		return
	}
	p := path(root, project, oracle)
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	unlock, held, err := filelock.TryLock(p, 5*time.Second)
	if err != nil || !held {
		return
	}
	defer unlock()
	at := now()
	var kept []record
	for _, r := range load(p) {
		if r.counts(m, at) {
			kept = append(kept, r)
		}
	}
	kept = append(kept, record{At: at, CPUs: m.CPUs, MemBytes: m.MemoryBytes,
		Cores: use.CPU.Seconds() / wall.Seconds(), PeakBytes: use.PeakBytes, Basis: use.Basis})
	if len(kept) > Keep {
		kept = kept[len(kept)-Keep:]
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil && fileshare.Replace(tmp, p) != nil {
		_ = os.Remove(tmp)
	}
}

// Read is the figure of the project's oracle on this machine m, and false when
// none counts: no run kept, all too old, or all measured on another machine.
func Read(root, project, oracle string, m machine.Reading) (Need, bool) {
	var n Need
	found := false
	at := now()
	rs := load(path(root, project, oracle))
	if len(rs) > Keep {
		rs = rs[len(rs)-Keep:]
	}
	for _, r := range rs {
		if !r.counts(m, at) {
			continue
		}
		found = true
		n.Cores = max(n.Cores, r.Cores)
		if r.PeakBytes > n.PeakBytes {
			n.PeakBytes, n.Basis = r.PeakBytes, r.Basis
		}
	}
	return n, found
}
