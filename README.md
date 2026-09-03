# metron

A lab report for a code change. Three readings, each against a reference range,
with an exit code you can gate on.

**No LLM, no network, no API key.** Every number comes from parsing your code and
running your tests. Same commit in, same numbers out.

Line coverage cannot tell a suite that checks results from one that merely
executes code — and agents are very good at writing the second kind. Complexity
nobody measures drifts up one branch at a time. The same job gets written three
ways because nothing counts it.

The name is Greek μέτρον, "a measure", the root of *metric* and *metrology*. The
Delphic maxim μέτρον ἄριστον — "measure is best" — is the argument: agent-written
code usually runs; what it lacks is measure.

## The problem, in one example

```
$ metron --since main --axes all

  METRON  main · 1 files · 18+

  reading                       value   reference
  ────────────────────────────────────────────────
  cognitive max                    3   ≤ 15      ✓    Discount
  cognitive Δ (existing funcs)     0   = 0       ✓
  orphaned symbols                 0   = 0       ✓
  duplicated work                  0   = 0       ✓
  mutation score                 20%   ≥ 70%     L
  test strength                  20%   ≥ 80%     L
  reach                         100%   ≥ 85%     ✓
  surviving mutants               12   = 0       H

  3 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
    pricing/pricing.go:12  no test caught this change to Discount (CONDITION_FORCE)
      - if tier == "gold" {
      + if false {
```

*(Abridged — a real run prints every reading in the table below.)*

**That code has 100% line coverage.** It scores 20. Replace the test with one that
actually asserts and it scores 100, at the same 100% coverage.

The readings say *which* problem it is: reach is 100%, so the tests do execute the
code; strength is 20%, so they execute it and check nothing.

## Install and run

```
go install github.com/yanmxa/metron/cmd/metron@latest

cd your-repo
metron --since main                 # complexity + graph, about a second
metron --since main --axes all      # adds mutation: runs your test suite
```

Requires Go 1.26+ and a git repository. The graph axis additionally needs a
[CodeGraph](https://github.com/colbymchenry/codegraph) index — `codegraph init`
enables it; without one that axis reports `n/a` and says why rather than quietly
passing.

```
metron [--since ref] [--axes complexity,graph,mutation|all] [--format table|json]
       [--fail-on headline|any|none] [--budget 5m] [--paranoid] [--fresh] [-C dir]
```

Exit codes: `0` all within range · `1` error · `2` a reading out of range ·
`3` budget spent, readings cover only a sample.

## What each reading means

| reading | out of range means |
| --- | --- |
| **mutation score** | The change is not held up by tests. The gate. |
| test strength | Tests run this code but assert too little about it. |
| reach | Much of the change is never executed by any test. |
| surviving mutants | Each one is a concrete edit no test noticed. Listed with a diff. |
| uncovered mutants | Code no test reaches at all. |
| non-viable rate | A diagnostic on *metron*, not on you — its generator is misfiring. |
| **cognitive max** | Some changed function is hard to read. Named in the output. |
| **cognitive Δ** | You made an existing function worse instead of extracting. |
| funcs over threshold | How many changed functions are over. |
| cognitive max (raw) | Same, without the Go error-guard discount. Comparable to gocognit. |
| **orphaned symbols** | New code nothing calls or references. |
| **duplicated work** | A new function does the same job as one that exists. |
| **bypassed paths** | New code calls something directly that everything else reaches via a wrapper. |
| unprecedented deps | A dependency direction this repository has never taken. |
| sibling divergence | A convention every neighbouring function follows, broken. |

Bold readings gate by default; the rest inform. Change what gates with
`--fail-on`, and the ranges themselves per repository.

**There is no composite score.** One weighted number hides which axis failed and
invites gaming.

## Three opinionated definitions

```
                   KILLED + TIMED_OUT
  mutation score = ─────────────────────────────────────────
                   KILLED + TIMED_OUT + SURVIVED + NOT_COVERED
```

**Uncovered mutants count against you.** The dominant failure in agent-written
code is 200 new lines with 20 tested well. Excluding them scores that
near-perfect, and it is gamed by writing one excellent test for one tiny
function. *Strength* asks "are the tests you wrote good tests"; the *score* asks
"is this change held up by tests". Only the second deserves a gate.

**Mutants that fail to compile do not count.** Those are an artifact of metron's
generator, not a property of your tests — counting them would penalise a file for
containing string concatenation. Reported separately instead.

**Go's error guards are scored twice.** `if err != nil { return err }` is 7.7% of
every branch keyword in the Go standard library and higher in application code. A
Go reader parses it as one token, not a branch, so counting it in full makes every
Go function look complex and the metric stops discriminating. The raw score stays
comparable with gocognit; the reference range is set against the adjusted one.

## Behaviour worth knowing

**A partial run never fails a build.** The sample is not the population, and a
tool that reports red on incomplete evidence teaches people to ignore it.

**It refuses to score a suite it cannot sample.** If the budget buys under a
quarter of the mutants, the axis reports `n/a` with the arithmetic rather than a
number derived from a handful.

**Resume is on by default.** Verdicts are content-addressed and flushed to
`.metron/` as they land, so an interrupted run continues instead of restarting:
8.3s cold, 0.26s fully cached. Touch one byte of source or test and the cache is
discarded — a stale verdict is far more dangerous than none. Reference ranges are
excluded from the key, since they change how a reading is judged, not what it is.
`--fresh` forces a re-measure.

## Why the numbers can be trusted

Cognitive complexity was cross-checked against
[gocognit](https://github.com/uudashr/gocognit) across all 528 functions in
`spf13/cobra`: **523 agree exactly**. The five that differ are one deliberate
divergence — the SonarSource specification raises the nesting level inside an
`else` body and gocognit does not.

The graph rules were tightened against a standard: six no-op edits to `cobra` —
one comment line added to an untouched function — must produce **zero** findings.
Getting there took three fixes, each from a real misfire:

- Go passes functions as values constantly (`return defaultUsageFunc`) and the
  index records no call edge for it, so orphan detection cross-checks real
  identifier use.
- A wrapper is not merely a popular caller; its target has to be *funnelled*.
- Only edges the change actually **introduced** count. Without comparing against
  the merge base, every call a merely-touched function has always made is
  reported.

The mutation axis has three traps that all fail toward a *passing* grade, so each
has a regression test: a build failure is shaped exactly like a test failure in
the JSON stream; vet runs by default and a vet failure looks like a build failure;
and concurrency wakes flaky tests that then read as kills.

[docs/mutation-design.md](docs/mutation-design.md) has the measurements behind
every one of these decisions.

## Development

```
go test ./...
go run ./cmd/metron --since HEAD~1 --axes all
```
