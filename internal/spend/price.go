package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// PricesFile is the name of the file in the jig home that adds to, or
// replaces, the built-in prices.
const PricesFile = "prices.yaml"

// Rates are one model's list prices in US dollars per million tokens.
type Rates struct {
	Input        float64
	Output       float64
	CacheWrite5m float64 // writing to the 5 minute cache
	CacheWrite1h float64 // writing to the 1 hour cache
	CacheRead    float64
}

// builtin holds the list prices of the models whose price is known, by
// normalized model id (see normalize). It is the one part of this package
// that goes stale: a model it does not list is reported as unpriced, and a
// jig home's prices.yaml can price it without a new jig.
var builtin = map[string]Rates{
	"claude-opus-4-5":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-1":   {15, 75, 18.75, 30, 1.50},
	"claude-opus-4":     {15, 75, 18.75, 30, 1.50},
	"claude-3-opus":     {15, 75, 18.75, 30, 1.50},
	"claude-sonnet-4-5": {3, 15, 3.75, 6, 0.30},
	"claude-sonnet-4":   {3, 15, 3.75, 6, 0.30},
	"claude-3-7-sonnet": {3, 15, 3.75, 6, 0.30},
	"claude-3-5-sonnet": {3, 15, 3.75, 6, 0.30},
	"claude-haiku-4-5":  {1, 5, 1.25, 2, 0.10},
	"claude-3-5-haiku":  {0.80, 4, 1, 1.60, 0.08},
	"claude-3-haiku":    {0.25, 1.25, 0.30, 0.50, 0.03},
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// normalize reduces a model id to the form prices are keyed by: lower case,
// without a context-window suffix ("[1m]"), the "-latest" alias or the
// snapshot date, so claude-opus-4-5-20251101 and claude-opus-4-5 are one.
func normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, '['); i >= 0 {
		m = m[:i]
	}
	m = strings.TrimSuffix(m, "-latest")
	return dateSuffix.ReplaceAllString(m, "")
}

// Prices are the rates a jig home adds to the built-in ones, by normalized
// model id. The zero value (nil) adds none.
type Prices map[string]Rates

// Lookup returns the rates of model: the jig home's, else the built-in ones.
func (p Prices) Lookup(model string) (Rates, bool) {
	k := normalize(model)
	if r, ok := p[k]; ok {
		return r, true
	}
	r, ok := builtin[k]
	return r, ok
}

// priceEntry is one model in prices.yaml. Input and output are required; the
// cache rates default to Anthropic's multiples of the input rate: writing to
// the 5 minute cache 1.25x, to the 1 hour cache 2x, reading 0.1x.
type priceEntry struct {
	Input        float64  `yaml:"input"`
	Output       float64  `yaml:"output"`
	CacheWrite   *float64 `yaml:"cache_write"`
	CacheWrite1h *float64 `yaml:"cache_write_1h"`
	CacheRead    *float64 `yaml:"cache_read"`
}

// LoadPrices reads <jigHome>/prices.yaml: a map from model id to its rates in
// US dollars per million tokens,
//
//	claude-opus-5:
//	  input: 5
//	  output: 25
//
// A jig home with no such file adds no prices. A file that does not parse, or
// prices a model without an input and an output rate, is an error.
func LoadPrices(jigHome string) (Prices, error) {
	path := filepath.Join(jigHome, PricesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]priceEntry
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("spend: parse %s: %w", path, err)
	}
	out := make(Prices, len(raw))
	for model, e := range raw {
		if e.Input <= 0 || e.Output <= 0 {
			return nil, fmt.Errorf("spend: %s: model %q needs an input and an output price above zero", path, model)
		}
		r := Rates{Input: e.Input, Output: e.Output, CacheWrite5m: 1.25 * e.Input, CacheWrite1h: 2 * e.Input, CacheRead: 0.1 * e.Input}
		if e.CacheWrite != nil {
			r.CacheWrite5m = *e.CacheWrite
		}
		if e.CacheWrite1h != nil {
			r.CacheWrite1h = *e.CacheWrite1h
		}
		if e.CacheRead != nil {
			r.CacheRead = *e.CacheRead
		}
		out[normalize(model)] = r
	}
	return out, nil
}
