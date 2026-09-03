# metron

A lab report for a code change. Three readings, each against a reference range,
with an exit code you can gate on.

Line coverage cannot tell a test suite that checks results from one that merely
executes code — and agents are very good at writing the second kind. Complexity
nobody measures drifts upward one branch at a time. The same job gets written
three ways because nothing counts it. All three are measurable, and what comes
out is a number rather than an opinion.

The name is Greek μέτρον, "a measure", the root of *metric* and *metrology*. The
Delphic maxim μέτρον ἄριστον — "measure is best" — is the argument: agent-written
code usually runs; what it lacks is measure.

## The point, in one example

```
$ metron --since main --axes all
  METRON  main · 1 files · 18+

  reading                       value   reference
  ────────────────────────────────────────────────
  cognitive max                    3   ≤ 15      ✓    Discount
  cognitive Δ (existing funcs)     0   = 0       ✓
  funcs over threshold             0   = 0       ✓
  cognitive max (raw)              3
  orphaned symbols                 0   = 0       ✓
  duplicated work                  0   = 0       ✓
  bypassed paths                   0   = 0       ✓
  unprecedented deps               0   = 0       ✓
  sibling divergence               0   = 0       ✓
  mutation score                 20%   ≥ 70%     L
  test strength                  20%   ≥ 80%     L
  reach                         100%   ≥ 85%     ✓
  surviving mutants               12   = 0       H
  uncovered mutants                0   = 0       ✓
  non-viable rate                 0%   ≤ 15%     ✓

  3 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITION_FORCE)
      - if total < 0 {
      + if true {
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
    …
```

**That code has 100% line coverage.** It scores 20. Replace the test with one
that actually asserts and it scores 100 — at the same 100% coverage.

The decomposition names the problem: reach is 100%, so the tests do execute the
code; strength is 20%, so they execute it and check nothing.

## The three axes

| axis | the question |
| --- | --- |
| **mutation** | Do the tests hold the code up? Break it and see whether anything notices. |
| **complexity** | How hard is it to read and extend — and how much worse did this change make it? |
| **graph** | Does it duplicate, orphan, or step around what already exists? |

**There is no composite score.** One weighted number hides which axis failed and
gets gamed. Each axis reports its own readings against its own ranges.

## Install

```
go install github.com/yanmxa/metron/cmd/metron@latest
```

The graph axis reads a [CodeGraph](https://github.com/colbymchenry/codegraph)
index — run `codegraph init` to enable it. Without one it reports `n/a` and says
why, rather than quietly passing.

## Usage

```
metron [--since ref] [--axes complexity,graph,mutation|all] [--format table|json]
       [--fail-on headline|any|none] [--budget 5m] [--paranoid] [--fresh] [-C dir]
```

`complexity` and `graph` run by default and take about a second. `mutation` runs
your test suite, so it is opt-in.

Exit codes: `0` all within range · `1` error · `2` a reading out of range ·
`3` budget spent, readings cover only a sample. **A partial run never fails a
build** — the sample is not the population, and a tool that reports red on
incomplete evidence teaches people to ignore it.

**Resume is on by default.** Verdicts are content-addressed and flushed to
`.metron/` as they land, so an interrupted run continues instead of restarting:
8.3s cold, 0.26s fully cached. Touch one byte of source or test and the whole
cache is discarded, because a stale verdict is far more dangerous than none.
Reference ranges are excluded from the key — they change how a reading is judged,
not what it is. `--fresh` forces a re-measure.

**It refuses to score a suite it cannot sample.** If the budget buys under a
quarter of the mutants, the axis reports `n/a` with the arithmetic instead of a
number derived from a handful.

## How the mutation score is defined

```
                   KILLED + TIMED_OUT
  score    = ─────────────────────────────────────────    ← headline, and the gate
             KILLED + TIMED_OUT + SURVIVED + NOT_COVERED

  strength = (KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED)
  reach    = 1 − NOT_COVERED / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)
```

**Uncovered mutants count against you.** The dominant failure in agent-written
code is 200 new lines with 20 tested well. Excluding them scores that
near-perfect, and it is gamed by writing one excellent test for one tiny
function. Strength asks "are the tests you wrote good tests"; the score asks "is
this change held up by tests". Only the second is worth gating on.

**Non-viable mutants do not.** A mutant that fails to compile is an artifact of
metron's generator, not a property of your tests — counting it would penalise a
file for containing string concatenation. It is reported separately.

A timeout counts as detected. Mutants are generated only inside the function
bodies the change touched, so a run takes tens of seconds rather than scanning
the repository.

## How cognitive complexity is defined

Computed natively over `go/ast`, following the SonarSource specification.
Cross-checked against [gocognit](https://github.com/uudashr/gocognit) over all
528 functions in `spf13/cobra`: **523 agree exactly**. The five that differ are
one deliberate divergence — the specification raises the nesting level inside an
`else` body and gocognit does not. Code inside an `else` really is one level
deeper for the reader.

**Go's error guards are scored twice, on purpose.** The canonical
`if err != nil { return err }` is 7.7% of every branch keyword in the Go standard
library and higher in application code. A Go reader parses it as one token, not a
branch; counting it in full makes every Go function look complex and the metric
stops discriminating. So the raw score stays comparable with gocognit, and the
reference range is set against the adjusted one. Only guards that purely bail out
are discounted — anything with an `else`, or that handles the error, is a real
branch.

**Δ is the reading that matters most.** It targets the habit of piling branches
into a function that already exists instead of extracting a new one. In the demo
above the absolute value sat inside its range; only Δ caught the change.

## Calibration

The graph rules were tightened against a standard: six no-op edits to `cobra` —
one comment line added to an untouched function — must produce zero findings.
Reaching zero took three fixes, each from a real misfire:

- Go passes functions as values constantly (`return defaultUsageFunc`) and the
  index records no call edge for it, so orphan detection cross-checks real
  identifier use.
- A wrapper is not merely a popular caller. Its target has to be *funnelled* —
  almost nothing else calls it directly.
- Only edges the change actually **introduced** count. Without comparing against
  the merge base, every call a merely-touched function has always made is
  reported.

The mutation axis has three traps that all fail toward a *passing* grade, so each
has a regression test: a build failure is shaped exactly like a test failure in
the JSON stream; vet runs by default and a vet failure looks like a build
failure; and concurrency wakes flaky tests that then read as kills.

[docs/mutation-design.md](docs/mutation-design.md) has the measurements behind
every one of these decisions.

## Development

```
go test ./...
go run ./cmd/metron --since HEAD~1 --axes all
```
