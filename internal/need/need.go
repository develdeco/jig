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
	// History is how many of an oracle's latest runs are kept: more than make
	// its figure, since the share rule (internal/slot) reads how its recent
	// runs fared next to each other.
	History = 30
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
	// WallSeconds and Shared are what the share rule reads: how long the run
	// took, and whether another heavy run held a share at any moment of it. A
	// record written before them has no wall time and is not read by it.
	WallSeconds float64 `json:"wall_s,omitempty"`
	Shared      bool    `json:"shared,omitempty"`
	// Cmd names the command the run ran (a hash): one oracle name runs as
	// several commands (one per workspace, one per repo), and the share rule
	// compares a run only with runs of the same command.
	Cmd string `json:"cmd,omitempty"`
}

// Run is one recorded run as the share rule reads it.
type Run struct {
	Wall     time.Duration
	Shared   bool
	CPUs     int
	MemBytes uint64
}

// path is the file of a project's oracle: the oracle's name goes through a
// hash since a name may hold characters a file name cannot.
func path(root, project, oracle string) string {
	sum := sha256.Sum256([]byte(oracle))
	return filepath.Join(root, "needs", project, hex.EncodeToString(sum[:8])+".json")
}

// commandKey is the short hash that names a command in a record.
func commandKey(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:8])
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

// Record keeps, as of at, a heavy run that ended on its own (a verdict, pass or fail; a
// run jig stopped is not recorded, since it did not finish using what it
// would): its wall time, whether it shared the machine with another heavy
// run, and what its process tree used. A run whose CPU time
// was not measured is not recorded. It is best effort: a figure that cannot be
// written leaves the next run unmeasured, which asks for the whole machine.
func Record(root, project, oracle, command string, m machine.Reading, at time.Time, wall time.Duration, use envrun.Usage, shared bool) {
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
	var kept []record
	for _, r := range load(p) {
		if r.counts(m, at) {
			kept = append(kept, r)
		}
	}
	kept = append(kept, record{At: at, CPUs: m.CPUs, MemBytes: m.MemoryBytes,
		Cores: use.CPU.Seconds() / wall.Seconds(), PeakBytes: use.PeakBytes, Basis: use.Basis,
		WallSeconds: wall.Seconds(), Shared: shared, Cmd: commandKey(command)})
	if len(kept) > History {
		kept = kept[len(kept)-History:]
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

// Read is the figure of the project's oracle on this machine m as of at, and false when
// none counts: no run kept, all too old, or all measured on another machine.
func Read(root, project, oracle string, m machine.Reading, at time.Time) (Need, bool) {
	var n Need
	found := false
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

// Runs are the recorded runs of the project's oracle command not older than
// MaxAge as of at, oldest first, whatever machine they were taken on (the rule
// that reads them compares the counts). A run with no recorded wall time, or
// of another command under the same oracle name, is left out.
func Runs(root, project, oracle, command string, at time.Time) []Run {
	var out []Run
	for _, r := range load(path(root, project, oracle)) {
		if r.WallSeconds <= 0 || r.Cmd != commandKey(command) || at.Sub(r.At) > MaxAge {
			continue
		}
		out = append(out, Run{Wall: time.Duration(r.WallSeconds * float64(time.Second)), Shared: r.Shared, CPUs: r.CPUs, MemBytes: r.MemBytes})
	}
	return out
}
