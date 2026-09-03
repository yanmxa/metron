# What each metric means

Every reading here is deterministic: same commit in, same number out. Each
section says what the metric is, how it is computed, what it is for, and shows
a worked example with real output.

The examples are small Go programs. You can reproduce any of them.

---

## mutation score

**The gate.** Whether the tests hold the code up.

```
(KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)
```
Reference range: **≥ 70%**

metron rewrites the code in small, deliberate ways — flipping a comparison,
dropping a returned error, forcing a branch — and runs the tests. A mutant is
*detected* if any test fails or hangs. The score is the fraction detected.

### Why it exists

Line coverage says a line ran. It cannot say anything ran *because* of it. A
test that calls a function and checks only that it did not panic gives 100%
coverage and pins nothing.

### Worked example

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

A test that drives every branch but asserts only `got >= 0`:

```
coverage: 100.0% of statements

  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed
```

Rewrite the test to assert exact values at the boundaries, and — at the **same**
100% coverage —

```
  mutation score     100%   ≥ 70%     ✓
    test strength    100%   ≥ 80%     ✓    0 of 15 mutants survived
```

### What you can do with it

Gate on it in CI. It is the one reading where a low number always means
something is genuinely wrong, and where the fix is always concrete: metron
prints the assertion each surviving mutant proves is missing.

---

## test strength and reach

**The decomposition.** *Which* testing problem you have.

```
strength = (KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED)      ≥ 80%
reach    = 1 − NOT_COVERED / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)  ≥ 85%
```

Strength drops uncovered mutants from the denominator, so it measures only the
code the tests actually run. Reach measures how much they run at all.

### Why it exists

A low mutation score has two completely different causes with two different
fixes, and the score alone cannot tell them apart.

| | reach | strength | what is wrong | what to do |
|---|---|---|---|---|
| untested code | **low** | high | tests never reach it | add cases |
| unasserted code | high | **low** | tests reach it and check nothing | add assertions |

### Worked example

```
  mutation score     0%   ≥ 70%     L
    test strength    0%   ≥ 80%     L    13 of 13 mutants survived
    reach           38%   ≥ 85%     L    21 never executed
```

Both are low: most of the package is never executed *and* what is executed is
never checked. Reach is the one to fix first — assertions on code nothing calls
would be wasted work.

---

## cognitive complexity

**How hard a function is to read.**

Computed over `go/ast` per the SonarSource specification: every construct that
breaks linear flow costs 1, plus 1 more for each level of nesting it sits
inside. Reference range: **≤ 15** for the worst changed function.

### Why it exists, and why not cyclomatic complexity

Cyclomatic complexity counts decisions. It cannot distinguish three decisions in
a row from three decisions inside one another, and those are not equally hard to
read.

### Worked example

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

Identical cyclomatic count. Cognitive complexity doubles, which matches the
experience of reading them.

### Go's error guards

`if err != nil { return err }` is 7.7% of every branch keyword in the Go
standard library and more in application code. A Go reader takes it as one
token, not a branch. Counting it in full makes every Go function look complex
and the metric stops discriminating, so metron discounts it — but only when the
guard purely bails out. Anything with an `else`, or that handles the error, is a
real branch and is counted.

The undiscounted score stays in `--format json` as
`complexity.cognitive_raw_max`, comparable with gocognit.

### What you can do with it

Find the function to extract from. The output names it, and the finding carries
its cyclomatic count, line count, fan-out, parameter count and nesting depth so
you can see *why* it scored what it did.

---

## cognitive Δ

**Whether a function got worse.**

```
score now − score at the merge base       (matched by name and receiver)
```
Only for functions that already existed. Reference range: **= 0**.

### Why it exists

This is the reading aimed squarely at how agents actually degrade a codebase.
They rarely write one monstrous function. They add a branch to an existing one,
then another, and each individual change looks reasonable.

### Worked example

A change adds a nested guard to `RangeArgs` in `spf13/cobra`:

```
  cognitive max         12   ≤ 15      ✓    RangeArgs
  cognitive Δ           +9   = 0       H    RangeArgs
```

**The absolute value passes.** 12 is comfortably inside a limit of 15. Only the
delta catches it — the function went from 3 to 12 in one change.

### What you can do with it

Gate on `Δ = 0` and a codebase cannot silently rot. Note it requires a base
revision, so `--all` reports it as unmeasured rather than as zero.

---

## redundant code

**Code that did not need to exist as written.**

Three rules, summed. Reference range: **= 0**.

| rule | fires when |
|---|---|
| `orphan` | no inbound call/reference/instantiate edge, **and** the identifier appears nowhere else in the source, **and** it is not a convention entry point (`main`, `init`, `Test*`, interface methods), **and** it is unexported or in a `main`/`internal` package |
| `near-duplicate` | callee-set Jaccard ≥ 0.6 **and** name-word overlap ≥ 0.3 **and** different files **and** both sides call ≥ 3 things |
| `reimplementation` | identical signature **and** name overlap ≥ 0.5 **and** different files **and** it never calls the original |

### Why the conditions are so specific

Each was added after a real misfire. Go passes functions as values constantly
(`return defaultUsageFunc`) and the index records no call edge for that, so the
orphan rule cross-checks real identifier use. Structural similarity alone
flagged `complete_text` against `complete_json` — sibling variants, not
duplication — so name overlap has to agree too.

### Worked example

```go
func Used() int { return helper() }
func helper() int { return 1 }

// orphan is never called and never referenced.
func orphan(xs []int) int { ... }
```

```
  redundant code        1   = 0       H    1 unreachable

  graph
    dead/dead.go:8  orphan is never reached
      no inbound edge in the graph, and the identifier appears nowhere else in the source
```

### What you can do with it

Delete the unreachable. For duplicates, the finding names both sides and their
locations, so you can pick which one survives.

---

## inconsistent code

**Code that does not fit what is already here.**

Three rules, summed. Reference range: **= 0**. Needs a base revision — only
edges the change *introduced* count.

| rule | fires when |
|---|---|
| `bypassed-wrapper` | a target is funnelled through a wrapper (wrapper has ≥ 3 callers, ≤ 4 callees, same directory, and the target has ≤ 1 other direct caller) and the new code calls the target directly |
| `layer-crossing` | a new edge's source directory → target directory pair has no precedent in the repository |
| `sibling-divergence` | ≥ 80% of ≥ 5 sibling functions in the directory follow a convention (`context.Context` first parameter, returning `error`) and the new one does not |

### Why "only new edges" matters

Without comparing against the base, every call a merely-touched function has
always made gets reported. Measured on `spf13/cobra`, six no-op edits — adding
one comment line to an untouched function — produced five findings before this
was fixed, and zero after.

### What you can do with it

Catch an agent reaching past the abstraction the codebase established, before
that becomes the new precedent.

---

## CRAP

**Which one to fix first.** Per function, not a reading, does not gate.

```
CRAP(f) = cyclomatic(f)² × (1 − mutationScore(f))³ + cyclomatic(f)
```
Conventional limit: **30**. Defined by Alberto Savoia in 2007 for Crap4j.

Complexity is forgiven when the code is pinned and punished hard when it is not.
Cyclomatic 10 scores 10 fully tested and 110 untested.

### How metron differs from the original

Crap4j feeds on line coverage — the number this whole tool exists to distrust.
metron substitutes the **per-function mutation score**, so a function with 100%
coverage and no assertions stays dangerous instead of scoring as safe. That is
exactly the case CRAP was invented to catch and the coverage-based version
misses.

### Worked example

```go
func Route(method, path string, admin bool) string {
	if method == "GET" {
		if path == "/health" { return "health" }
		return "read"
	}
	if admin && method == "DELETE" { return "purge" }
	if method == "POST" { return "write" }
	return "deny"
}
```

With a test that calls every combination and asserts only `!= ""`:

```
  cognitive max       6   ≤ 15      ✓    Nested
  mutation score     0%   ≥ 70%     L

  complexity
    risky/risky.go:4  Route is the riskiest thing in this change
      CRAP 42 (0% of mutants caught) — over the usual limit of 30 · cognitive 6 · cyclomatic 6
```

**Complexity passed.** 6 is far inside a limit of 15, and the complexity axis
would never have mentioned `Route`. Only combined with the mutation score does
it become the most dangerous function present — and CRAP promotes it into the
report for exactly that reason.

### What you can do with it

Use it as the work order. Highest CRAP is where a change is most likely to break
something silently: hardest to reason about, least likely to be caught.

A function with no mutants gets no score rather than an invented one. Without
the mutation axis the panel says `risk ranking needs the mutation axis` instead
of printing nothing.

---

## Diagnostics

Numbers that do not earn a row but reach `--format json`:

| key | what it is |
|---|---|
| `complexity.cyclomatic_max` | classic decision count; comparable with gocyclo |
| `complexity.cognitive_raw_max` | cognitive score before the error-guard discount |
| `complexity.crap_max` | worst CRAP in the run |
| `complexity.fan_out_max` | most distinct callees in one function |
| `complexity.params_max` / `lines_max` / `nesting_max` | interface width, length, depth |
| `complexity.over_threshold` / `functions` | counts |
| `graph.orphans` / `duplicates` / `bypassed` / `layer_crossings` / `sibling_divergence` | the individual rule counts behind the two merged readings |
| `graph.changed_symbols` | how many symbols were in scope |
| `mutation.killed` / `survived` / `timed_out` / `not_covered` / `not_viable` / `skipped` | the raw tally |
| `mutation.not_viable_rate` | a diagnostic on metron's own generator, not on your code — above 15% means its operator gating is underperforming |

Each axis also emits `funcs`: per-function records carrying path, function name,
line, cyclomatic, cognitive, delta, and mutants/detected. That is what makes
CRAP computable across two axes.

---

## Mutation operators

Ten operators, each producing a specific instruction when its mutant survives.

| operator | rewrites | says, when it survives |
|---|---|---|
| `CONDITIONALS_BOUNDARY` | `<`↔`<=`, `>`↔`>=` | assert the behaviour at the boundary `X == Y` |
| `CONDITIONALS_NEGATION` | comparison operators inverted | assert behaviour that changes when `expr` is negated |
| `INVERT_LOGICAL` | `&&`↔`\|\|` | assert a case where exactly one side of `expr` holds |
| `ARITHMETIC_BASE` | `+`↔`-`, `*`↔`/` | assert the value `expr` computes |
| `INVERT_ASSIGNMENTS` | `+=`↔`-=`, `*=`↔`/=` | assert the value `x` computes |
| `INCREMENT_DECREMENT` | `++`↔`--` | assert the value of `x` after this runs |
| `INVERT_LOOP_CTRL` | `break`↔`continue` | assert behaviour with further iterations |
| `NIL_ERROR_RETURN` | `return err` → `return nil` | assert that this path returns a non-nil error |
| `REMOVE_STATEMENT` | deletes a call | assert the effect of `f()` |
| `CONDITION_FORCE` | condition → `true`/`false` | assert the behaviour that depends on `cond` being true/false |

`NIL_ERROR_RETURN` is Go-specific and in neither gremlins nor go-mutesting. It
has the highest hit rate on agent-written code, which generates error plumbing
constantly and almost never tests it.

Guidance is always phrased as an assertion to add, never as a claim about what
the tests do. A survivor cannot distinguish "this input is never supplied" from
"it is supplied and the result is never checked", and asserting the first when
it is the second sends you to write a test that already exists.

---

## Mutant outcomes

| outcome | meaning | in the score's denominator? |
|---|---|---|
| `KILLED` | a test failed | yes, as detected |
| `TIMED_OUT` | the mutation hung the suite | yes, as detected — it *was* noticed |
| `SURVIVED` | every test passed | yes, as undetected |
| `NOT_COVERED` | pre-filtered as unreachable, never run | **yes** — see the mutation score section |
| `NOT_VIABLE` | did not compile | no — metron's fault, not yours |
| `SKIPPED` | budget ran out first | no |
| `ERRORED` | the harness failed | no, and never counted as detected |

For how these are told apart in the `go test -json` stream — and the three traps
that all fail toward a passing grade — see
[mutation-design.md](mutation-design.md).
