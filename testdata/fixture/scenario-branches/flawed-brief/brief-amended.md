# Fixture ticket brief

## Goal

Give the fixture repo two small, real fixes so the frontier loop has
something to chew on: a broken function to repair, a caller to add once it is
fixed, a wording decision to make, and an environment class to smoke-test.

## Slice A — failing clamp

`alpha.Clamp` is committed broken: it ignores the upper bound. Fix it so
`TestClamp` passes without changing its signature.

## Slice B — clamp callers

Add a small percentage-range caller on top of the now-working `Clamp`, plus a
test that exercises it end to end.

## Slice C — greeting decision

Add a `casual bool` parameter to `beta.Greet`: when true, use a shorter,
informal greeting; when false, keep the current formal wording. Update
`TestGreet` to cover both.

## Slice D — rig smoke

Exercise the `rig` environment class end to end: bring it up, use it, and add
one small marker into the beta workspace to prove the slice ran.
