package spend

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// BudgetFile is the name of a ticket's budget record, in the ticket folder.
const BudgetFile = "budget.yaml"

// NearFraction is how much of a limit makes a ticket "near" its budget.
const NearFraction = 0.8

// BudgetPath is where ticket's budget record is.
func BudgetPath(st *store.Store, ticket string) string {
	return filepath.Join(st.TicketDir(ticket), BudgetFile)
}

// ReadBudget reads ticket's budget record. recorded is false, with no error,
// when the ticket has none. A record that exists but sets no limit is a
// budget the ticket was deliberately given none of (see WriteBudget).
func ReadBudget(st *store.Store, ticket string) (b project.Budget, recorded bool, err error) {
	data, err := os.ReadFile(BudgetPath(st, ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return project.Budget{}, false, nil
		}
		return project.Budget{}, false, err
	}
	if err := yaml.Unmarshal(data, &b); err != nil {
		return project.Budget{}, false, fmt.Errorf("spend: parse %s: %w", BudgetPath(st, ticket), err)
	}
	return b, true, nil
}

// WriteBudget records b as ticket's budget, replacing any earlier one, under
// the file's lock, atomically. A limit of zero is no limit; a Budget with both
// zero is still recorded, as an empty file, which says the ticket has no
// budget on purpose, so a default is not recorded over it later.
func WriteBudget(st *store.Store, ticket string, b project.Budget) error {
	if b.Tokens < 0 || b.USD < 0 || math.IsNaN(b.USD) || math.IsInf(b.USD, 0) {
		return fmt.Errorf("spend: a budget cannot be negative: %d tokens, %v usd", b.Tokens, b.USD)
	}
	path := BudgetPath(st, ticket)
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	data, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, data)
}

// Limited reports whether b sets a limit at all.
func Limited(b project.Budget) bool { return b.Tokens > 0 || b.USD > 0 }

// ProjectDefault is the budget the project's jig.yaml marker gives a ticket to
// start from: the nearest marker above the first of dirs that has one. ok is
// false when there is no marker, or it sets no budget.
func ProjectDefault(dirs ...string) (b project.Budget, ok bool) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, m, found, err := project.FindMarker(dir); err == nil && found {
			if m.Budget != nil && Limited(*m.Budget) {
				return *m.Budget, true
			}
			return project.Budget{}, false
		}
	}
	return project.Budget{}, false
}

// State is how a ticket's spend stands against its budget.
type State string

const (
	// StateNone: the ticket has no limit to hold spend against.
	StateNone State = "none"
	// StateOK: under NearFraction of every limit.
	StateOK State = "ok"
	// StateNear: at NearFraction of a limit or more, and not over any.
	StateNear State = "near"
	// StateOver: past a limit.
	StateOver State = "over"
)

// Verdict is the result of holding spend against a budget.
type Verdict struct {
	State State
	// Ratio is the largest share of a limit spent (1.5 is half as much
	// again as the limit), 0 when there is no limit.
	Ratio float64
}

// Check holds t against b: tokens against the token limit, dollars against
// the dollar limit, each only when set.
func Check(t Totals, b project.Budget) Verdict {
	if !Limited(b) {
		return Verdict{State: StateNone}
	}
	var ratio float64
	if b.Tokens > 0 {
		ratio = float64(t.Tokens()) / float64(b.Tokens)
	}
	if b.USD > 0 {
		ratio = max(ratio, t.USD/b.USD)
	}
	switch {
	case ratio > 1:
		return Verdict{State: StateOver, Ratio: ratio}
	case ratio >= NearFraction:
		return Verdict{State: StateNear, Ratio: ratio}
	}
	return Verdict{State: StateOK, Ratio: ratio}
}

// ParseTokens reads a token count as a person writes it: 5000000, 5M, 1.5m,
// 200k, 2G (k is a thousand, M a million, G a billion).
func ParseTokens(s string) (int64, error) {
	t := strings.TrimSpace(strings.ToLower(s))
	mult := 1.0
	switch {
	case strings.HasSuffix(t, "k"):
		mult, t = 1e3, strings.TrimSuffix(t, "k")
	case strings.HasSuffix(t, "m"):
		mult, t = 1e6, strings.TrimSuffix(t, "m")
	case strings.HasSuffix(t, "g"):
		mult, t = 1e9, strings.TrimSuffix(t, "g")
	}
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n*mult > math.MaxInt64/2 {
		return 0, fmt.Errorf("%q is not a token count (use 5000000, 5M or 200k)", s)
	}
	return int64(math.Round(n * mult)), nil
}

// ParseUSD reads a dollar amount: 20, 12.50, $12.50.
func ParseUSD(s string) (float64, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "$")
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("%q is not a dollar amount (use 20 or 12.50)", s)
	}
	return n, nil
}

// Compact writes a token count short: 950, 1.2k, 31.2M, 2G.
func Compact(n int64) string {
	f, unit := float64(n), ""
	switch {
	case n >= 1e9:
		f, unit = f/1e9, "G"
	case n >= 1e6:
		f, unit = f/1e6, "M"
	case n >= 1e3:
		f, unit = f/1e3, "k"
	default:
		return strconv.FormatInt(n, 10)
	}
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0") + unit
}

// Dollars writes an amount as dollars with cents: $12.50.
func Dollars(usd float64) string { return "$" + strconv.FormatFloat(usd, 'f', 2, 64) }
