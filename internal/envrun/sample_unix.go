//go:build !windows

package envrun

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sampleEvery is the gap between two samples of a run's resident memory.
const sampleEvery = time.Second

// sampleLimit bounds one ps call, so a wedged ps cannot hold the run's end.
const sampleLimit = 5 * time.Second

// sampler watches the resident memory of one process group while a run lives.
type sampler struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once

	peakKiB uint64 // the largest sum seen; read after done is closed
}

// startSampler samples the group pgid once now and then every sampleEvery
// until finish is called. It needs ps; with none it samples nothing.
func startSampler(pgid int) *sampler {
	s := &sampler{stop: make(chan struct{}), done: make(chan struct{})}
	ps, err := exec.LookPath("ps")
	if err != nil {
		close(s.done)
		return s
	}
	go s.run(ps, pgid)
	return s
}

func (s *sampler) run(ps string, pgid int) {
	defer close(s.done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	tick := time.NewTicker(sampleEvery)
	defer tick.Stop()
	for {
		s.sample(ctx, ps, pgid)
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
	}
}

// sample takes one reading and keeps it if it is the largest so far.
func (s *sampler) sample(ctx context.Context, ps string, pgid int) {
	ctx, cancel := context.WithTimeout(ctx, sampleLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, ps, "-A", "-o", "pgid=,rss=").Output()
	if err != nil {
		return
	}
	if kib := groupRSSKiB(string(out), pgid); kib > s.peakKiB {
		s.peakKiB = kib
	}
}

// finish stops the sampler and returns the largest group sum seen, in bytes,
// 0 when none was read.
func (s *sampler) finish() uint64 {
	s.once.Do(func() { close(s.stop) })
	<-s.done
	return s.peakKiB * 1024
}

// groupRSSKiB sums the resident sizes (KiB) in the lines of psOut ("pgid rss"
// per process) whose process group is pgid. A line that does not parse is
// skipped.
func groupRSSKiB(psOut string, pgid int) uint64 {
	var sum uint64
	for _, line := range strings.Split(psOut, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		g, err := strconv.Atoi(f[0])
		if err != nil || g != pgid {
			continue
		}
		if rss, err := strconv.ParseUint(f[1], 10, 64); err == nil {
			sum += rss
		}
	}
	return sum
}
