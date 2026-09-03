# metron

English · [简体中文](README.zh.md)

[![ci](https://github.com/yanmxa/metron/actions/workflows/ci.yml/badge.svg)](https://github.com/yanmxa/metron/actions/workflows/ci.yml)
[![go reference](https://pkg.go.dev/badge/github.com/yanmxa/metron.svg)](https://pkg.go.dev/github.com/yanmxa/metron)
[![go report card](https://goreportcard.com/badge/github.com/yanmxa/metron)](https://goreportcard.com/report/github.com/yanmxa/metron)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Measures what an AI agent just wrote, against explicit metrics, and hands it
back a concrete instruction for every gap it finds.**

Agents produce code that passes review and does not hold. They write tests that
execute every line and assert nothing. They pile branches into a function instead
of extracting one. They rewrite a helper that already exists because they could
not find it. None of that shows up in a diff, and none of it shows up in coverage.

metron measures it, then says what to do about it — in terms an agent can act on
and re-verify.

**No LLM, no network, no API key.** Every number comes from parsing the code and
running its tests. Same commit in, same numbers out — which is what makes it safe
to put in a loop, and safe to gate on.

## The loop

An agent has just written `Discount` and a test for it. The test covers **100% of
statements**.

```
$ metron --since main --axes all

  reading            value   reference
  ─────────────────────────────────────
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  2 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
      assert the behaviour at the boundary total == 0
```

Reach is 100%: the tests really do run every line. Strength is 20%: they run it
and check almost nothing. And the last line is not a complaint — it is a task.
Every surviving mutant carries the assertion it proves is missing, derived from
the operator and its operands.

The agent acts on those instructions and runs metron again:

```
  mutation score     100%   ≥ 70%     ✓
    test strength    100%   ≥ 80%     ✓    0 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  all within range
```

Exit 0. Coverage was 100% before and after; only metron could tell the two apart.

## Wiring it into an agent

```
metron --since main --axes all --format json
```

Three things make it usable inside a loop:

- **`detail` on every finding is an instruction**, not a diagnosis — "assert the
  behaviour at the boundary `total == 0`", not "coverage is low".
- **Exit codes are the stop condition.** `0` done · `2` something still out of
  range · `3` incomplete, do not treat as pass · `1` error.
- **Re-running is cheap.** Verdicts are content-addressed and cached, so an
  iteration that changes one function does not re-measure the rest: 8.3s cold,
  0.26s fully cached.

Give the agent a rule and it can close the loop itself:

```markdown
After changing Go code, run `metron --since main --axes all --format json`.
For every finding, do what its `detail` says, then run it again.
Stop when the exit code is 0. Never edit metron's thresholds to make it pass.
```

That last sentence matters. The gate is only worth having if the agent cannot
move it.

## How it compares

| | metron | gocyclo / gocognit | go test -cover | gremlins |
| --- | --- | --- | --- | --- |
| complexity | ✅ cognitive **and** delta vs base | ✅ absolute only | — | — |
| does the test suite hold up | ✅ mutation, diff-scoped | — | ⚠️ coverage only | ✅ whole repo |
| dead / duplicated code | ✅ | — | — | — |
| tells you what to write | ✅ per finding | — | — | — |
| resumable, cached | ✅ | n/a | n/a | — |
| one gate for all of it | ✅ | — | — | — |

The nearest thing to metron is running gocognit, coverage and gremlins
separately and reading three reports. The difference is that the readings are
combined — CRAP only exists because complexity and mutation are measured
together — and that every finding carries the change that closes it.

## Analysing existing code

```
metron --all --axes complexity,graph
```

`--all` measures the whole repository instead of a diff. It answers strictly
less — with no base revision there is no "how much worse did this get", and no
way to tell which dependency is newly drawn, so those readings report `n/a`
rather than guessing.

The repository also ships a `code-health` skill that runs this analysis, ranks
findings by CRAP, and writes a prioritised report:
[.claude/skills/code-health](.claude/skills/code-health/SKILL.md).

## The readings

Full definitions, worked examples, and what each one is for:
**[docs/metrics.md](docs/metrics.md)**.

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
