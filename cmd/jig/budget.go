package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/spend"
)

// The usage lines of `jig budget`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	budgetTokensUsage  = "limit on tokens (input, output and cache, all sessions); 5M, 200k or 5000000, 0 for none"
	budgetUSDUsage     = "limit on dollars at list price, all sessions; 0 for none"
	budgetDefaultUsage = "record the project's default budget (the jig.yaml budget) on the ticket"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"budget", "set or show the spend budget of a ticket", []flagSpec{
			{"tokens", budgetTokensUsage, false},
			{"usd", budgetUSDUsage, false},
			{"default", budgetDefaultUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 390,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdBudget(e, args, stdout)
		},
	})
}

// cmdBudget implements `jig budget [<ticket>] [--tokens N] [--usd D]
// [--default]`. With a flag it records the ticket's budget in its budget.yaml:
// a limit not named keeps the ticket's own, and a ticket with no budget
// starts from the project's default (the budget of the jig.yaml marker), so
// `--tokens 5M` in a project that defaults to 40 dollars gives the ticket
// both. `--default` records the project default as it is. Without a flag it
// shows the budget and the spend so far.
//
// A budget is a record, not a limit jig enforces: a session is told when its
// ticket is near or over budget (the Stop context of `jig observe`), status
// shows it, and nothing is stopped.
func cmdBudget(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "budget")
	tokensFlag := fs.String("tokens", "", budgetTokensUsage)
	usdFlag := fs.String("usd", "", budgetUSDUsage)
	useDefault := fs.Bool("default", false, budgetDefaultUsage)
	flags := addWorkFlags(fs, false, false)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, err := atMostOne(positionals, "jig budget", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	var tokens int64
	var usd float64
	if given["tokens"] {
		if tokens, err = spend.ParseTokens(*tokensFlag); err != nil {
			return renderErr(stdout, &axi.Error{Msg: "--tokens: " + err.Error(), Code: "VALIDATION_ERROR"})
		}
	}
	if given["usd"] {
		if usd, err = spend.ParseUSD(*usdFlag); err != nil {
			return renderErr(stdout, &axi.Error{Msg: "--usd: " + err.Error(), Code: "VALIDATION_ERROR"})
		}
	}

	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	setting := given["tokens"] || given["usd"] || *useDefault
	if setting {
		if err := w.St.Sync(); err != nil {
			return renderErr(stdout, err)
		}
	}
	current, recorded, err := spend.ReadBudget(w.St, w.Ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	projectBudget, haveProject := spend.ProjectDefault(w.Where.Cwd, w.Where.RepoDir, w.Where.Root)

	if !setting {
		return showBudget(stdout, w, current, recorded, projectBudget, haveProject)
	}

	next := current
	if !recorded && haveProject {
		next = projectBudget
	}
	if *useDefault {
		if !haveProject {
			return renderErr(stdout, &axi.Error{
				Msg:  "this project sets no default budget: its jig.yaml has no budget",
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Name the limits: `jig budget %s --tokens 5M --usd 20`", w.Ticket)},
			})
		}
		next = projectBudget
	}
	if given["tokens"] {
		next.Tokens = tokens
	}
	if given["usd"] {
		next.USD = usd
	}
	if err := spend.WriteBudget(w.St, w.Ticket, next); err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Push(fmt.Sprintf("%s: budget", w.Ticket)); err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout, budgetBlock("budget", w.Ticket, next, "ticket"),
		axi.Help(fmt.Sprintf("`jig status %s` shows it against what the ticket has spent", w.Ticket)))
	return 0
}

// showBudget prints the budget the ticket stands under and what it has spent.
func showBudget(stdout io.Writer, w work, current project.Budget, recorded bool, projectBudget project.Budget, haveProject bool) int {
	shown, source := current, "ticket"
	help := fmt.Sprintf("`jig budget %s --tokens 5M --usd 20` sets one", w.Ticket)
	switch {
	case recorded && !spend.Limited(current):
		source = "ticket (none on purpose)"
	case recorded:
		help = fmt.Sprintf("`jig budget %s --tokens N --usd N` changes it; 0 takes a limit away", w.Ticket)
	case haveProject:
		shown, source = projectBudget, "project default (not yet recorded on the ticket)"
		help = fmt.Sprintf("`jig budget %s --default` records it on the ticket; the ticket's first recorded spend does too", w.Ticket)
	default:
		source = "none"
	}
	blocks := []string{budgetBlock("budget", w.Ticket, shown, source)}
	if spent, ok, err := spendBlock(w.St, w.Ticket); err != nil {
		return renderErr(stdout, err)
	} else if ok {
		blocks = append(blocks, spent)
	}
	blocks = append(blocks, axi.Help(help))
	axi.Render(stdout, blocks...)
	return 0
}

// budgetBlock renders a ticket's budget: each limit, or none.
func budgetBlock(label, ticket string, b project.Budget, source string) string {
	tokens, usd := "none", "none"
	if b.Tokens > 0 {
		tokens = strconv.FormatInt(b.Tokens, 10)
	}
	if b.USD > 0 {
		usd = strconv.FormatFloat(b.USD, 'f', 2, 64)
	}
	return axi.KV(label, [][2]string{{"ticket", ticket}, {"tokens", tokens}, {"usd", usd}, {"source", source}})
}
