package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// stopCase is one way to end a running jig process: stop does it, skip (when
// set) says why the case is not run.
type stopCase struct {
	name string
	stop func(*os.Process) error
	skip string
}

// oracleScript is the oracle the test commits: it starts a long-running
// child, records its own pid and the child's (the Windows pid under Git's sh,
// which numbers processes of its own), says it is ready, and waits on it.
const oracleScript = `pid() { cat "/proc/$1/winpid" 2>/dev/null || echo "$1"; }
pid $$ >> "$ORACLE_PIDS"
sleep 600 &
pid $! >> "$ORACLE_PIDS"
: > "$ORACLE_PIDS.ready"
wait
`

const oracleManifest = `workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
oracles:
  test: "sh ./oracle.sh"
`

// TestKilledJigStopsItsOracle: an oracle's tree lives and dies with the jig
// process that runs it. A jig gate, or a verification worker, stopped
// mid-oracle (Windows: terminated; Unix: SIGTERM or SIGHUP) leaves no process
// of the oracle running, however the shell that started the oracle's tests
// went.
func TestKilledJigStopsItsOracle(t *testing.T) {
	for _, how := range []string{"gate", "worker"} {
		for _, sc := range stopCases {
			t.Run(how+"/"+sc.name, func(t *testing.T) {
				if sc.skip != "" {
					t.Skip(sc.skip)
				}
				t.Parallel()
				fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
				dropEnvFromSlices(t, fx)
				h := newHarness(t, fx, env, "s-kill")
				pids := filepath.Join(t.TempDir(), "pids")
				env = env.with("ORACLE_PIDS=" + pids)
				h.Branch(fx.Branch)
				sha := h.Commit("a", "slow oracle", map[string]string{
					".claude/jig.yaml": oracleManifest,
					"oracle.sh":        oracleScript,
				})
				h.Push(fx.Branch)

				var p *jigProc
				switch how {
				case "gate":
					p = startJig(t, env, fx.StoreDir, "gate", fx.Ticket, "--early")
				default:
					p = startJig(t, env, h.Clone, "verify", "--worker", "--store", fx.StoreDir,
						"--repo-dir", h.Clone, "--slice", "a", fx.Ticket, sha)
				}

				waitFor(t, 2*time.Minute, "the oracle to start its child", func() bool {
					_, err := os.Stat(pids + ".ready")
					return err == nil
				})
				var oraclePIDs []int
				for _, f := range strings.Fields(string(readFileOrFatal(t, pids))) {
					n, err := strconv.Atoi(f)
					if err != nil {
						t.Fatalf("pid file holds %q: %v", f, err)
					}
					oraclePIDs = append(oraclePIDs, n)
				}
				t.Cleanup(func() {
					for _, pid := range oraclePIDs {
						if pidAlive(pid) {
							killTree(pid)
						}
					}
				})

				if err := sc.stop(p.cmd.Process); err != nil {
					t.Fatalf("stop jig with %s: %v", sc.name, err)
				}
				select {
				case <-p.done:
				case <-time.After(30 * time.Second):
					t.Fatalf("jig %s was still running 30s after %s", how, sc.name)
				}
				// The stop is no verdict on the commit: the journal holds no
				// failed oracle line for it, whatever the stopped jig wrote,
				// except one that says jig's stop ended the run.
				for _, l := range readJournal(t, fx, fx.Ticket) {
					if l.Event == journal.EventOracle && l.Outcome == "fail" && l.Cause != journal.CauseStopped {
						t.Errorf("the journal holds a failed oracle line after jig %s was stopped: %+v", how, l)
					}
				}
				for _, pid := range oraclePIDs {
					pid := pid
					if !pollUntil(15*time.Second, func() bool { return !pidAlive(pid) }) {
						t.Errorf("oracle process %d is still running after jig %s was stopped by %s (oracle pids %v)", pid, how, sc.name, oraclePIDs)
					}
				}
			})
		}
	}
}
