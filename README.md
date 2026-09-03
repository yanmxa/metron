# metron

English · [简体中文](README.zh.md)

A lab report for a code change: seven readings, each against a reference range,
with an exit code you can gate on.

**No LLM, no network, no API key.** Every number comes from parsing your code and
running your tests. Same commit in, same numbers out.

## The problem, in one example

```
$ metron --since main --axes all

  METRON  main · 1 files · 18+

  reading            value   reference
  ─────────────────────────────────────
  cognitive max         3   ≤ 15      ✓    Discount
  cognitive Δ           0   = 0       ✓
  redundant code        0   = 0       ✓
  inconsistent code     0   = 0       ✓
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  2 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITION_FORCE)
      - if total < 0 {
      + if true {
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
    …
```

**That code has 100% line coverage.** It scores 20. Replace the test with one that
actually asserts and it scores 100, at the same 100% coverage.

The two indented readings say *which* problem it is. Reach is 100%, so the tests
do execute the code. Strength is 20%, so they execute it and check nothing.

## Install and run

```
go install github.com/yanmxa/metron/cmd/metron@latest

cd your-repo
metron --since main                 # complexity + graph, about a second
metron --since main --axes all      # adds mutation: runs your test suite
```

Needs Go 1.26+ and a git repository. The graph axis also needs a
[CodeGraph](https://github.com/colbymchenry/codegraph) index — `codegraph init`
enables it. Without one that axis reports `n/a` and says why, rather than quietly
passing.

Exit codes: `0` all within range · `1` error · `2` a reading out of range ·
`3` budget spent, readings cover only a sample.

## The seven readings

| reading | out of range means |
| --- | --- |
| **mutation score** | The change is not held up by tests. This is the gate. |
| ↳ test strength | Tests run this code but assert too little about it. |
| ↳ reach | Much of the change is never executed by any test. |
| **cognitive max** | A changed function is hard to read. The output names it. |
| **cognitive Δ** | You made an existing function worse instead of extracting. |
| **redundant code** | Something you wrote did not need to exist: unreachable, or a duplicate of what is already there. |
| **inconsistent code** | Something does not fit the codebase: it bypassed a wrapper, drew a dependency nothing else draws, or broke a convention its neighbours follow. |

Every reading is followed by the specific findings behind it — a surviving mutant
comes with the exact diff no test noticed, an orphan with its file and line.

The last two are deliberately coarse. Five separate graph counters said the same
thing five ways; you act on all of them by reading the finding underneath. The
individual counts are still in `--format json`, along with raw cognitive
complexity and metron's own generator health.

**There is no composite score.** One weighted number hides which reading failed
and invites gaming.

## Two opinionated definitions

```
                   KILLED + TIMED_OUT
  mutation score = ─────────────────────────────────────────
                   KILLED + TIMED_OUT + SURVIVED + NOT_COVERED
```

**Uncovered code counts against you.** The dominant failure in agent-written code
is 200 new lines with 20 tested well. Leaving uncovered mutants out of the
denominator scores that near-perfect, and it is gamed by writing one excellent
test for one tiny function. *Strength* asks "are the tests you wrote good tests".
The *score* asks "is this change held up by tests". Only the second deserves a
gate. (Mutants that fail to compile are excluded — those are metron's fault, not
yours, and are reported separately.)

**Go's error guards are discounted.** `if err != nil { return err }` is 7.7% of
every branch keyword in the Go standard library and more in application code. A Go
reader takes it as one token, not a branch; counting it in full makes every Go
function look complex and the metric stops discriminating. Only guards that purely
bail out are discounted — anything with an `else`, or that handles the error, is a
real branch. The undiscounted score is kept in the JSON, comparable with gocognit.

## Behaviour worth knowing

**A partial run never fails a build.** The sample is not the population, and a
tool that reports red on incomplete evidence teaches people to ignore it.

**It refuses to score a suite it cannot sample.** If the budget buys under a
quarter of the mutants, that axis reports `n/a` with the arithmetic rather than a
number derived from a handful.

**Resume is on by default.** Verdicts are content-addressed and flushed to
`.metron/` as they land, so an interrupted run continues instead of restarting:
8.3s cold, 0.26s fully cached. Touch one byte of source or test and the cache is
discarded — a stale verdict is far more dangerous than none. `--fresh` forces a
re-measure.

## Why the numbers can be trusted

Cognitive complexity was cross-checked against
[gocognit](https://github.com/uudashr/gocognit) across all 528 functions in
`spf13/cobra`: **523 agree exactly.** The five that differ are one deliberate
divergence — the SonarSource specification raises the nesting level inside an
`else` body, and gocognit does not.

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
