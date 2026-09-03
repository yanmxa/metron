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

## The readings

Seven readings sit in the table; five of them gate. Each is followed by the
specific findings behind it.

### mutation — do the tests hold the code up?

Mutants are generated inside the function bodies the change touched, each one a
single deliberate edit. A mutant is *detected* if any test fails or hangs.

| reading | computed as | out of range means |
| --- | --- | --- |
| **mutation score** | `detected / (detected + survived + uncovered)` | The change is not held up by tests. This is the gate. |
| ↳ test strength | `detected / (detected + survived)` | Tests run this code but assert too little about it. |
| ↳ reach | `1 − uncovered / total` | Much of the change is never executed by any test. |

```
  mutation
    pricing/pricing.go:9  no test caught this change to Quote (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
```


Each survivor carries the assertion it proves is missing, derived from the
operator and its operands:

```
  mutation
    pricing/pricing.go:9  no test caught this change to Quote (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
      assert the behaviour at the boundary total == 0
```

That last line is the point. `--format json` carries it as `detail`, so an agent
iterating against metron gets a concrete, verifiable task rather than a number it
has to interpret. It is derived, not generated — no model is involved, and the
same commit always produces the same instruction.

It is phrased as an assertion to add, never as a claim about what the tests do. A
survivor cannot tell "this input is never supplied" from "it is supplied and the
result is never checked", and saying the first when it is the second sends you to
write a test that already exists.

**Uncovered code counts against the score.** The dominant failure in
agent-written code is 200 new lines with 20 tested well. Leaving uncovered
mutants out of the denominator scores that near-perfect, and it is gamed by
writing one excellent test for one tiny function. *Strength* asks "are the tests
you wrote good tests"; the *score* asks "is this change held up by tests". Only
the second deserves a gate. Mutants that fail to compile are excluded entirely —
those are metron's fault, not yours, and are reported separately.

### complexity — how hard is it to read and change?

Cognitive complexity per the SonarSource specification, computed over `go/ast`:
each construct that breaks linear flow costs 1, plus 1 for every level of nesting
it sits inside.

**Go's error guards are discounted.** `if err != nil { return err }` is 7.7% of
every branch keyword in the Go standard library and more in application code. A
Go reader takes it as one token, not a branch; counting it in full makes every Go
function look complex and the metric stops discriminating. Only guards that
purely bail out are discounted — anything with an `else`, or that handles the
error, is a real branch. The undiscounted score stays in the JSON, comparable
with gocognit.

| reading | computed as | out of range means |
| --- | --- | --- |
| **cognitive max** | highest adjusted score among changed functions | A changed function is hard to read. The output names it. |
| **cognitive Δ** | score now minus score at the merge base, matched by name and receiver | You made an existing function worse instead of extracting. |

```
  complexity
    pricing/pricing.go:8  Quote (Δ +9, was 3)
      CRAP 54 (10% of mutants caught) — over the usual limit of 30 · cognitive 7 · cyclomatic 8
```

Cyclomatic complexity, fan-out, parameter count, line count and nesting depth are
computed for every changed function too. They appear on each finding and in
`--format json`, but do not get readings of their own.

### graph — does it fit what is already here?

Read from a CodeGraph index: the symbols in the repository and the edges between
them, compared against the merge base so only edges the change *introduced*
count.

| reading | computed as | out of range means |
| --- | --- | --- |
| **redundant code** | unreachable symbols + near-duplicates | Something you wrote did not need to exist. |
| **inconsistent code** | bypassed wrappers + unprecedented dependency directions + broken local conventions | Something does not fit the codebase. |

```
  graph
    pricing/pricing.go:22  unusedHelper is never reached
      no inbound edge in the graph, and the identifier appears nowhere else in the source
```

These two are deliberately coarse. Five separate counters said the same thing five
ways, and you act on all of them by reading the finding underneath. The individual
counts stay in `--format json`.

**There is no composite score.** One weighted number hides which reading failed
and invites gaming.

## CRAP — which one do I fix first?

```
CRAP(f) = cyclomatic(f)² × (1 − mutationScore(f))³ + cyclomatic(f)
```

[Change Risk Analysis and Predictions](https://www.artima.com/weblogs/viewpost.jsp?thread=210575), defined by
Alberto Savoia in 2007 and implemented in Crap4j. Complexity is forgiven when the code is pinned and punished hard when
it is not: cyclomatic 10 scores 10 fully tested, 110 untested. Over 30 is the
conventional limit.

metron departs from the original in one place, and it is the important one.
Crap4j feeds on line coverage — the number this tool exists to distrust. Here the
coverage term is the **per-function mutation score**, so a function with 100%
coverage and no assertions stays dangerous instead of scoring as safe. That is
precisely the case CRAP was invented to catch and the coverage-based version
misses.

It is **not an eighth reading**. It is per-function and its job is ranking, not
gating, so it annotates and orders the complexity findings — and promotes a
function the complexity axis passed over. In the example above, cyclomatic 8
clears a threshold of 15 comfortably; at 10% of mutants caught it is still the
worst thing in the change, and nothing else would have said so.

CRAP needs both axes. Run without `--axes all` and the panel says so rather than
printing nothing:

```
  all within range · risk ranking needs the mutation axis — add --axes all
```

A function with no mutants gets no score, not an invented one.

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
