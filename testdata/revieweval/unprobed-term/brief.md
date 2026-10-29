## Repro

`cart.Total` applies the discount to each line item and truncates each line
to whole cents before summing. `cart/cart_test.go:TestTotalRoundsOnceOnTheOrder`
already encodes the repro: three items at 333 cents each, 10% off.

```
go test ./cart/...
--- FAIL: TestTotalRoundsOnceOnTheOrder (0.00s)
    cart_test.go:10: Total = 897 cents, want 899
```

## Expected vs actual

- Actual: each 333-cent line is discounted and floor-divided individually
  (`333*90/100 = 299`), then summed: `299*3 = 897`.
- Expected: the discount is taken once on the order's pre-discount total
  (`999`), then rounded to the nearest cent: `999*90/100 = 899.1` -> `899`.

## Regression window

Introduced with `cart.Total` itself. There is no prior correct behavior to
restore; this is a fix to the original implementation, not a regression from
a later change.

## Decision: rounding tie-break [user-confirmed]

Round-half-up (away from zero) on the exact half-cent. `discountPct` and
line totals are always non-negative, so there is no sign ambiguity to
resolve. This matches ordinary billing/retail rounding and needs no extra
API surface.

## Decision: clamp out-of-range percentages [user-confirmed]

`discountPct < 0` clamps to `0` (no discount); `discountPct > 100` clamps to
`100` (free). No error is returned - `Total` stays a pure function with its
current signature `Total(items []Item, discountPct int) int`. Clamping is
silent: a caller passing a bad percentage gets the clamped price, not a panic
or error value, since `Total` has no error return today and widening its
signature is out of scope for this bug fix.

## Out of scope

- Changing `Total`'s signature (e.g. returning an error for an invalid
  percentage) - clamping covers the stated bug without an API change.
- Rounding modes other than round-half-up.
- Per-line pricing/display concerns (e.g. showing a per-line discounted
  price) - `Total` only ever returns the order's final total.

## Seams

**Public interfaces:**
- `cart.Total(items []Item, discountPct int) int` - the only exported
  function in the package; its return value is the order's price in cents.

**Critical paths:**
- Pricing a multi-item order with a discount percentage returns the
  once-applied, nearest-cent-rounded total (the REPRO case above).
- Pricing with a `discountPct` outside `[0, 100]` returns the same total as
  the nearest clamped boundary (`<0` behaves as `0`, `>100` behaves as
  `100`).
