package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// recordBuilderProbe is `jig probe` with no gate round open (ADR 0036): the
// session that holds a slice records a probe or a skip for it as a journal
// line of the ticket, with the blob of the file in its worktree, so an edit
// after the probe makes it stale. The slice is the one `jig verify` takes for
// the session: JIG_SLICE, else the slice its latest claim holds, and only while
// no release has undone that claim. The next round reads it with the earlier
// rounds' probes.
func recordBuilderProbe(e env, stdout io.Writer, st *store.Store, cfg project.Config, jigHome, ticket string, dr verifydeliver.Draft) int {
	dr, err := verifydeliver.CheckBuilderDraft(dr)
	if err != nil {
		return renderErr(stdout, err)
	}
	cwd, err := e.getwd()
	if err != nil {
		return renderErr(stdout, err)
	}
	where, err := locate.Locate(jigHome, cwd, locate.Opts{
		StoreFlag: st.Root, Ticket: ticket, Getenv: e.getenv, NoMap: true, Memo: e.memo,
	})
	if err != nil {
		return renderErr(stdout, err)
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	slice := where.Slice
	if _, held := claimHolder(lines, slice); slice == "" || !held {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("no review round is open for %s and this session holds no slice of it", ticket),
			Code: "REVIEW_NOT_OPEN",
			Help: []string{
				fmt.Sprintf("Claim the slice you are building to record your own probe: `jig claim %s --slice <slice>`", ticket),
				fmt.Sprintf("Or open a round to probe as its reviewer: `jig gate %s` (or `jig review context %s`)", ticket, ticket),
			},
		})
	}
	repoName, _, err := verifyRepo(cfg, where.Repo)
	if err != nil {
		return renderErr(stdout, err)
	}
	dir, err := verifyDir(e.memo, cwd)
	if err != nil {
		return renderErr(stdout, err)
	}
	file := verifydeliver.BuilderFile(dr)
	rel := file
	if len(cfg.Repos) > 1 {
		var ok bool
		if rel, ok = strings.CutPrefix(file, repoName+"/"); !ok {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("file %q is not in repo %s, the clone this worktree is of", file, repoName),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Name it as %s/<path>, from the clone of the repo it is in", repoName)},
			})
		}
	}
	blob, err := gitx.Run(dir, "hash-object", "--", rel)
	if err != nil || blob == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("file %q is not in this worktree", file),
			Code: "VALIDATION_ERROR",
			Help: []string{"Name a file of the change, as a path in the repo"},
		})
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	line := verifydeliver.BuilderLine(slice, where.Session, blob, stamp(e), dr)
	if err := journal.Append(st, ticket, line); err != nil {
		return renderErr(stdout, err)
	}
	kind := "probe"
	if dr.Skip != nil {
		kind = "skip"
	}
	if err := st.Push(fmt.Sprintf("%s: slice %s %s", ticket, slice, kind)); err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("probe", [][2]string{
			{"ticket", ticket}, {"slice", slice}, {"kind", kind}, {"file", file}, {"line", strconv.Itoa(lineOf(dr))}, {"recorded", "builder"},
		}),
		axi.Help("Record the next probe, or add a test for a survivor and probe it again; `jig verify` shows what is current, stale and survived, and the next gate round reads them"),
	)
	return 0
}

// lineOf is the line of a builder's probe, or where a skip starts.
func lineOf(dr verifydeliver.Draft) int {
	if dr.Probe != nil {
		return dr.Probe.Line
	}
	return dr.Skip.From
}
