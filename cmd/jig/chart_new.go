package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"chart", "make a chart: jig chart new <name> --title <t>", []flagSpec{
			{"title", "chart title (required)", false},
			{"issue", "id of the issue ticket the chart plans", false},
			{"need", "what the person needs: mints the chart's issue with this text as its body", false},
			{"key", "key to mint the issue under with --need or --item", false},
			{"item", "a tracker item that exists (48, #48, owner/repo#48 or its URL): makes its issue and chart and binds the two", false},
			{"json", "with --item: print one JSON object instead of blocks", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 121,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdChart(e, args, stdout)
		},
		FlagSet: "chart new",
		Probe:   []string{"new"},
	})
}

// chartNameRule is the shape of a name `jig chart new` accepts. It is lower
// case only: two chart folders that differ in case are one folder on a volume
// that ignores case (sameChartFile).
var chartNameRule = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]*[a-z0-9])?$`)

// maxChartNameLen bounds a new chart's name.
const maxChartNameLen = 64

// cmdChart implements `jig chart new <name> --title <t>`: it makes the chart
// the way `jig ticket new` makes a ticket. It writes the skeleton of
// charts/<name>/ once, lands it with Store.Claim, then runs the checkpoint
// sync, so the chart's issue exists before any ticket graduates under it.
func cmdChart(e env, args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (new)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub == "" {
		// requirePositional passed a leading -h/--help through.
		sub = "new"
	}
	if sub != "new" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown chart subcommand %q; only \"new\" is supported", sub),
			Code: "VALIDATION_ERROR",
			Help: []string{chartNewUsage},
		})
	}

	fs := newFlagSet(e, "chart new")
	title := fs.String("title", "", "chart title (required)")
	issueFlag := fs.String("issue", "", "id of the issue ticket the chart plans")
	needFlag := fs.String("need", "", "what the person needs: mints the chart's issue with this text as its body")
	keyFlag := fs.String("key", "", "key to mint the issue under with --need or --item")
	storeFlag := fs.String("store", "", "explicit store path")
	itemFlag := fs.String("item", "", "a tracker item that exists (48, #48, owner/repo#48 or its URL): makes its issue and chart and binds the two")
	jsonOut := fs.Bool("json", false, "with --item: print one JSON object instead of blocks")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	positionals, handled, err := parseWithPositionals(stdout, fs, rest)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	name, err := exactlyOne(positionals, "jig chart new", "chart name")
	if err != nil {
		return renderErr(stdout, withHelp(err, chartNewUsage))
	}
	itemSet, needSet := false, false
	fs.Visit(func(f *flag.Flag) {
		itemSet = itemSet || f.Name == "item"
		needSet = needSet || f.Name == "need"
	})
	if *jsonOut && !itemSet {
		return renderErr(stdout, &axi.Error{Msg: "--json is for --item: the bind it prints", Code: "VALIDATION_ERROR", Help: []string{chartNewUsage}})
	}
	if itemSet {
		return chartNewItem(e, stdout, name, *itemFlag, *title, *keyFlag, needSet, *issueFlag, *jsonOut, *projectFlag, *storeFlag)
	}
	heading := strings.TrimSpace(*title)
	if heading == "" {
		return renderErr(stdout, &axi.Error{Msg: "jig chart new requires --title", Code: "VALIDATION_ERROR", Help: []string{chartNewUsage}})
	}
	if strings.ContainsAny(heading, "\r\n") {
		return renderErr(stdout, &axi.Error{Msg: "--title must be one line", Code: "VALIDATION_ERROR", Help: []string{"Put the chart's longer story in charts/<name>/map.md, under Destination"}})
	}
	if err := checkNewChartName(name); err != nil {
		return renderErr(stdout, err)
	}

	if needSet && strings.TrimSpace(*needFlag) == "" {
		return renderErr(stdout, &axi.Error{Msg: "--need must not be empty: it is the issue's body", Code: "VALIDATION_ERROR", Help: []string{chartNewUsage}})
	}
	if needSet && *issueFlag != "" {
		return renderErr(stdout, &axi.Error{Msg: "--need and --issue exclude each other: --need makes the issue, --issue names one that exists", Code: "VALIDATION_ERROR", Help: []string{chartNewUsage}})
	}

	st, cfg, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	mintKey := ""
	if needSet {
		// An undeclared --key is refused before anything is minted.
		if mintKey, err = cfg.ResolveKey(*keyFlag); err != nil {
			return renderErr(stdout, err)
		}
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if err := chartNameTaken(st, name); err != nil {
		return renderErr(stdout, err)
	}
	issue := ""
	issueMinted := false
	if needSet {
		// The name was checked above and no chart holds it, so a refused name
		// has minted nothing. The slug is the title's, as `jig ticket new`
		// gives it.
		issue, err = st.Claim(
			func() (string, []string, error) {
				id, err := st.Mint(mintKey, store.Ticket{Title: heading, Body: strings.TrimSpace(*needFlag), Slug: store.Slug(heading), Kind: store.KindIssue})
				if err != nil {
					return "", nil, err
				}
				return id, []string{st.TicketRelDir(id)}, nil
			},
			func(id string) string { return fmt.Sprintf("%s: new ticket", id) },
		)
		if err != nil {
			return renderErr(stdout, err)
		}
		issueMinted = true
	} else if *issueFlag != "" {
		if issue, err = checkNewChartIssue(st, *issueFlag); err != nil {
			return renderErr(stdout, err)
		}
	}

	// Claim commits the chart's folder alone and, on a store with an origin,
	// pushes it alone. Its write checks the name again, so a chart or ticket
	// another clone pushed meanwhile is refused after the retry's pull.
	// Nothing goes in the session ledger: a chart is not a ticket.
	_, err = st.Claim(
		func() (string, []string, error) {
			if err := chartNameTaken(st, name); err != nil {
				return "", nil, err
			}
			if issue != "" {
				if _, err := checkNewChartIssue(st, issue); err != nil {
					return "", nil, err
				}
			}
			if err := st.CreateChartWithIssue(name, heading, issue); err != nil {
				return "", nil, err
			}
			return name, []string{st.ChartRelDir(name)}, nil
		},
		func(id string) string { return fmt.Sprintf("chart %s: new chart", id) },
	)
	if err != nil {
		if issueMinted {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("issue %s was made but its chart %q was not: %v", issue, name, err),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Make the chart for it: jig chart new %s --title %q --issue %s", name, heading, issue)},
			})
		}
		return renderErr(stdout, err)
	}

	// Claim never runs the checkpoint hook itself, so this is the chart's own
	// sync: the mirror opens its issue now, before any ticket graduates under it.
	st.RunCheckpointHook()

	fields := [][2]string{{"name", name}, {"title", heading}, {"folder", filepath.Join(st.Root, st.ChartRelDir(name))}}
	if issueMinted {
		fields = append(fields, [2]string{"issue", issue})
	}
	axi.Render(stdout,
		axi.KV("chart", fields),
		axi.Help(
			fmt.Sprintf("Fill charts/%s/map.md and add the chart's tickets to charts/%s/tickets.yaml", name, name),
			fmt.Sprintf("Check them with `jig validate %s`: it lists every problem and mints nothing", name),
			fmt.Sprintf("Run `jig graduate %s` to create the tickets", name),
		),
	)
	return 0
}

// chartNewUsage is the help line of a refusal for a missing name or title.
const chartNewUsage = "Run `jig chart new <name> --title \"<title>\" [--need \"<what is needed>\" | --issue <ID>]`"

// withHelp is err with line as its Help when it is a *axi.Error with none.
func withHelp(err error, line string) error {
	var ae *axi.Error
	if !errors.As(err, &ae) || len(ae.Help) > 0 {
		return err
	}
	withLine := *ae
	withLine.Help = []string{line}
	return &withLine
}

// checkNewChartName refuses a name `jig chart new` cannot give a chart: one
// validateChartName refuses, one outside chartNameRule or longer than
// maxChartNameLen, and one checkTicketID refuses, because chartNamed never
// reads such a word as a chart.
func checkNewChartName(name string) error {
	invalid := func(reason string) error {
		return &axi.Error{
			Msg:  fmt.Sprintf("chart name %q is not valid: %s", name, reason),
			Code: "VALIDATION_ERROR",
			Help: []string{"Choose a name such as \"checkout-coupons\""},
		}
	}
	const rule = `use lower-case letters, digits, "-" and "_", starting and ending with a letter or digit, at most 64 characters`
	if validateChartName(name) != nil || len(name) > maxChartNameLen || !chartNameRule.MatchString(name) {
		return invalid(rule)
	}
	return nil
}

// chartNameTaken is the refusal for a name that cannot be a new chart: a
// ticket id or alias holds it (a ticket wins over a chart name, so the chart
// could never be reached by its name), or charts/<name> exists.
func chartNameTaken(st *store.Store, name string) error {
	if wordIsATicket(st, name) {
		return &axi.Error{
			Msg:  fmt.Sprintf("%q is a ticket of the store: a ticket id or alias wins over a chart name, so choose another name", name),
			Code: "VALIDATION_ERROR",
			Help: []string{"Choose another name for the chart"},
		}
	}
	taken, err := st.ChartFolderTaken(name)
	if err != nil {
		return err
	}
	if taken {
		return &axi.Error{
			Msg:  fmt.Sprintf("chart %q already exists in the store at %s", name, st.Root),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Edit charts/%s/map.md and charts/%s/tickets.yaml, or choose another name", name, name)},
		}
	}
	return nil
}
