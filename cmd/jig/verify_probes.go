package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verify"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// showSliceProbes prints, after a slice's run, the probes recorded for it
// (ADR 0036): how many are current, stale and survived at the run's head, and
// a row for each stale or surviving one. A fix slice prints it with zeros when
// it has no probe; any other slice prints it only when it has one. It is a fact
// shown after the result: it changes neither the exit code nor what the run
// covers, and a probe it cannot read is left unshown, not an error of the run.
func showSliceProbes(d verify.Deps, req verify.Request, st *store.Store, cfg project.Config, stdout io.Writer) {
	if req.Slice == "" {
		return
	}
	slices, err := st.ReadSlices(req.Ticket)
	if err != nil {
		return
	}
	fix := false
	for _, s := range slices {
		if s.ID == req.Slice {
			fix = s.FromGate > 0
		}
	}
	head, err := gitx.RevParse(d.Clone, "HEAD")
	if err != nil {
		return
	}
	got, err := verifydeliver.BuilderProbesAt(st, req.Ticket, req.Slice, d.Repo, d.Clone, head, len(cfg.Repos) > 1)
	if err != nil || (!fix && got.Total == 0) {
		return
	}
	blocks := []string{axi.KV("probes", [][2]string{
		{"slice", req.Slice},
		{"current", strconv.Itoa(got.Current)},
		{"stale", strconv.Itoa(got.Stale)},
		{"survived", strconv.Itoa(got.Survived)},
	})}
	if len(got.Rows) > 0 {
		rows := make([][]string, len(got.Rows))
		for i, r := range got.Rows {
			rows[i] = []string{fmt.Sprintf("%s:%d", r.File, r.Line), r.Outcome, r.State, r.Mutation}
		}
		blocks = append(blocks,
			axi.Table("probe_rows", []string{"file:line", "outcome", "state", "mutation"}, rows),
			axi.Help(fmt.Sprintf("Add a test for each survivor and probe it again, and probe a stale term again after its edit: `jig probe %s --seam S --file F --line N --mutation M --command C --outcome caught|survived`", req.Ticket)),
		)
	}
	axi.Render(stdout, blocks...)
}
