# metron

English · [简体中文](README.zh.md)

[![ci](https://github.com/yanmxa/metron/actions/workflows/ci.yml/badge.svg)](https://github.com/yanmxa/metron/actions/workflows/ci.yml)
[![go reference](https://pkg.go.dev/badge/github.com/yanmxa/metron.svg)](https://pkg.go.dev/github.com/yanmxa/metron)
[![go report card](https://goreportcard.com/badge/github.com/yanmxa/metron)](https://goreportcard.com/report/github.com/yanmxa/metron)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A large share of code is now written by an AI. Test-driven development is the
usual way to keep that honest: say what the code must do, then make it do it.

But TDD assumes something it cannot check — that the tests it produced are worth
having. Three things go wrong, and none of them show up in a diff.

---

## 1. Are the tests actually any good?

An agent asked for tests will produce tests. They will run, they will pass, and
your coverage report will be green. That report answers "was this line
executed", which is not the question you meant to ask.

Here is a function and a test an agent wrote for it:

```go
func Discount(total int, tier string) (int, error) {
	if total < 0 {
		return 0, ErrNegative
	}
	if tier == "gold" {
		return total * 80 / 100, nil
	}
	if total > 100 {
		return total - 10, nil
	}
	return total, nil
}
```

```go
func TestDiscount(t *testing.T) {
	for _, tc := range []struct{ total int; tier string }{
		{200, "gold"}, {200, "std"}, {50, "std"}, {-1, "std"},
	} {
		got, err := Discount(tc.total, tc.tier)
		if tc.total < 0 { if err == nil { t.Fatal("want error") }; continue }
		if got < 0 { t.Fatalf("negative result %d", got) }
	}
}
```

**Coverage: 100% of statements.** Every branch runs. And the test asserts almost
nothing — change `total * 80 / 100` to `total * 90 / 100` and it still passes.

### Mutation testing answers the question coverage cannot

Break the code on purpose, then see whether the tests notice. metron rewrites the
changed code in small deliberate ways — flip a comparison, drop a returned error,
force a branch — and runs the suite against each rewrite. A rewrite nothing
notices is a gap.

```
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed
```

The two indented readings say *which* problem it is. **Reach** is 100%: the tests
really do run every line. **Strength** is 20%: they run it and check nothing.
Those are different failures with different fixes, and a single number hides
which one you have.

Rewrite the test to assert exact values and it scores **100** — at the same 100%
coverage. Coverage cannot tell those two suites apart. This can.

### And it tells you what to write

A surviving mutant is not a complaint. It is a specification for the missing
test, and which one follows mechanically from the operator:

```
  pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
    - if total < 0 {
    + if total <= 0 {
    assert the behaviour at the boundary total == 0
```

That last line is derived, not generated — no model involved, same commit in,
same instruction out. It is what makes this usable by an agent rather than only
by a person.

---

## 2. Will anyone be able to change it in six months?

Getting code written is the short part. It gets read, extended and debugged for
far longer, and an agent optimising for "make the test pass" has no stake in any
of that.

### Cognitive complexity, not cyclomatic

The usual measure counts decision points. It cannot tell three decisions in a row
from three decisions inside one another — and those are not equally hard to read:

```go
func Flat(a, b, c bool) int {          func Nested(a, b, c bool) int {
	n := 0                                     n := 0
	if a { n++ }                               if a {
	if b { n++ }                                       if b {
	if c { n++ }                                               if c { n++ }
	return n                                           }
}                                              }
                                               return n
                                       }
```

```
  Flat     cognitive=3   cyclomatic=4
  Nested   cognitive=6   cyclomatic=4
```

Identical cyclomatic count. Cognitive complexity doubles, because it charges a
penalty for every level of nesting — which is what actually makes code hard to
hold in your head.

### The delta matters more than the absolute value

Agents rarely write one monstrous function. They add a branch to an existing one,
then another, and each individual change looks reasonable. Here is a real change
to `spf13/cobra`:

```
  cognitive max         12   ≤ 15      ✓    RangeArgs
  cognitive Δ           +9   = 0       H    RangeArgs
```

**The absolute value passes.** 12 is comfortably inside a limit of 15. Only the
delta catches it — that function went from 3 to 12 in one change. Gate on `Δ = 0`
and a codebase cannot silently rot.

### CRAP: complexity you cannot verify is the dangerous kind

Complexity alone is not risk. A gnarly function with a suite that pins every
branch is fine; a middling one nothing checks is where a change quietly breaks
something. [CRAP](https://www.artima.com/weblogs/viewpost.jsp?thread=210575)
combines the two:

```
CRAP(f) = cyclomatic(f)² × (1 − tested(f))³ + cyclomatic(f)
```

Crap4j's original uses line coverage for `tested`. metron uses that function's
**mutation score** instead — because coverage is the number the first section
established cannot be trusted.

```
  cognitive max       6   ≤ 15      ✓          ← complexity says this is fine

  complexity
    risky.go:4  Route is the riskiest thing in this change
      CRAP 42 (0% of mutants caught) — over the usual limit of 30 · cyclomatic 6
```

Neither reading alone flags `Route`. Complexity clears its limit; the mutation
score is just a number about the package. Only together do they say: *this is
where to look first.*

---

## 3. Does it fit what is already there?

The hardest failure to see is not incorrect code. It is code that is perfectly
correct and should not exist — a helper rewritten because the agent could not
find the one already there, a wrapper stepped around, a dependency drawn in a
direction nothing else in the repository draws.

No amount of testing catches this. The code works. It is the *shape* of the
repository that got worse, and you cannot see shape from inside one diff.

### A graph of the whole repository

metron reads a [CodeGraph](https://github.com/colbymchenry/codegraph) index —
every symbol and every edge between them — and compares the change against it.

**Redundant code** — something that did not need to exist:

```
  redundant code        1   = 0       H    1 unreachable

  graph
    dead.go:8  orphan is never reached
      no inbound edge in the graph, and the identifier appears nowhere else
```

**Inconsistent code** — something that does not fit:

- calling a target directly when everything else reaches it through a wrapper
- drawing a dependency in a direction the repository has no precedent for
- breaking a convention every neighbouring function follows

Only edges the change **introduced** count. Without that comparison, every call a
merely-touched function has always made gets reported — on `cobra`, six no-op
edits produced five findings before this was fixed, and zero after.

---

## Putting it together

Three questions, seven readings, one exit code:

| | question | readings |
| --- | --- | --- |
| **mutation** | do the tests hold it up? | `score`, `strength`, `reach` |
| **complexity** | can it be changed later? | `cognitive max`, `cognitive Δ` |
| **graph** | does it fit what exists? | `redundant`, `inconsistent` |

Plus CRAP, which ranks findings rather than gating.

**There is no composite score.** One weighted number hides which axis failed and
invites gaming.

**No LLM, no network, no API key.** Every number comes from parsing the code and
running its tests. Same commit in, same numbers out — which is what makes it safe
to put in a loop, and safe to gate on.

## Install

```bash
# a binary — no Go toolchain needed
curl -fsSL https://raw.githubusercontent.com/yanmxa/metron/main/install.sh | sh

# or, if you have Go
go install github.com/yanmxa/metron/cmd/metron@latest
```

Needs a git repository, and Go 1.26+ only if you build from source. The graph
axis also needs a [CodeGraph](https://github.com/colbymchenry/codegraph) index —
`codegraph init` enables it. Without one that axis reports `n/a` and says why,
rather than quietly passing.

## Run

```bash
cd your-repo
metron init                         # optional: calibrate the ranges to this repo
metron --since main                 # complexity + graph, about a second
metron --since main --axes all      # adds mutation: runs your test suite
metron --all                        # measure the whole repository, not a change
```

`metron init` measures first, then writes a `metron.json` whose complexity limit
is **today's worst function** with a delta of zero. Existing complexity is
tolerated; none of it may grow. A tool that fails on the first run with no change
to blame gets switched off before it has said anything useful.

Exit codes: `0` all within range · `1` error · `2` a reading out of range ·
`3` budget spent, readings cover only a sample.

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

### Install the skill

The instructions above are one document, [`agent/metron.md`](agent/metron.md).
Every assistant reads instructions from a different path in a different wrapper,
so the installer writes the same body wherever yours looks for it:

```bash
curl -fsSL https://raw.githubusercontent.com/yanmxa/metron/main/install.sh | sh -s -- --skill
```

| assistant | file |
| --- | --- |
| Claude Code | `.claude/skills/metron/SKILL.md` |
| Cursor | `.cursor/rules/metron.mdc` |
| Windsurf | `.windsurf/rules/metron.md` |
| GitHub Copilot | `.github/copilot-instructions.md` |
| Codex CLI, Amp, others | `AGENTS.md` |

With no argument it installs for whichever assistants it finds in the repository,
falling back to `AGENTS.md`. `--agent all` writes every one. The `AGENTS.md`
section is delimited, so re-running replaces it rather than appending, and leaves
the rest of your file alone.

The rule it gives the agent ends with one line that matters more than the rest:

> Never edit `metron.json` thresholds, delete tests, or add `//nolint` to make a
> reading pass.

A gate is only worth having if the thing being gated cannot move it.

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

`--all` answers strictly less than `--since`. With no base revision there is no
"how much worse did this get", and no way to tell which dependency is newly
drawn, so those readings report `n/a` rather than guessing. Never present that
absence as a pass.

## Every reading, in one table

| reading | out of range means |
| --- | --- |
| **mutation score** | The change is not held up by tests. This is the gate. |
| ↳ test strength | Tests run this code but assert too little about it. |
| ↳ reach | Much of the change is never executed by any test. |
| **cognitive max** | A changed function is hard to read. The output names it. |
| **cognitive Δ** | You made an existing function worse instead of extracting. |
| **redundant code** | Something is unreachable, or duplicates what already exists. |
| **inconsistent code** | Something bypasses a wrapper, draws an unprecedented dependency, or breaks a local convention. |
| CRAP *(per function)* | Complexity weighted by how poorly tested it is. Over 30 is the conventional limit. Ranks findings; does not gate. |

Bold readings gate by default; change that with `--fail-on`. Every finding is
printed with the specific change that closes it.

**Full definitions, the exact formulas, and a reproducible worked example for
each: [docs/metrics.md](docs/metrics.md)** ([简体中文](docs/metrics.zh.md)).

Numbers with no row of their own — cyclomatic complexity, fan-out, parameter
count, nesting depth, the individual graph rule counts, the raw mutant tally —
are all in `--format json` under `diagnostics`.

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
