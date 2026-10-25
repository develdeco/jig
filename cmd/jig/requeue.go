package main

import (
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/ready"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"requeue", "requeue slices touched by a brief edit, or one stalled/env-blocked slice", []flagSpec{
			{"from-brief-diff", "requeue slices whose brief section hash changed", false},
			{"slice", "requeue one stalled or env-blocked slice by id", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 150,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdRequeue(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdRequeue implements `jig requeue <ticket> --from-brief-diff` and
// `jig requeue <ticket> --slice <id>`.
func cmdRequeue(e env, args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet(e, "requeue")
	fromBriefDiff := fs.Bool("from-brief-diff", false, "requeue slices whose brief section hash changed")
	sliceFlag := fs.String("slice", "", "requeue one stalled or env-blocked slice by id")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, _, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireOpen(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	if err := requireSlices(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	if *sliceFlag != "" {
		if err := ready.RequeueSlice(st, ticket, *sliceFlag); err != nil {
			return renderErr(stdout, err)
		}
		axi.Render(stdout,
			axi.Table("requeued", []string{"id"}, idRows([]string{*sliceFlag})),
			axi.Help(hintOrFallback(st, ticket)),
		)
		return 0
	}

	touched, err := ready.Requeue(st, ticket, *fromBriefDiff)
	if err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout,
		axi.Table("requeued", []string{"id"}, idRows(touched)),
		axi.Help(hintOrFallback(st, ticket)),
	)
	return 0
}
