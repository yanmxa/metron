# The mutation axis: what was measured

Every number here was measured on go1.26.2 / darwin arm64 / 10 cores. The
fixture is `spf13/cobra` at HEAD — 36 Go files, 261 top-level tests in the root
package — plus small purpose-built modules for the traps.

**The native engine works.** The overlay does not bust the build cache: a cold
build costs 3.68s, and every subsequent mutant ~0.9s. A realistic diff-scoped run
of 7 mutants completes end to end in 4.8s, or 2.2s at two workers.

---

## Three traps that silently corrupt the score

Ordered by severity. All three fail in the same direction — **toward a passing
grade** — which is the worst direction available.

### 1. A build failure is shaped exactly like a test failure

A mutant that does not compile exits 1 and emits a package-level
`{"Action":"fail"}` byte-for-byte identical to what a real test failure produces.
Classifying on the exit code, or on "the package failed", scores **every**
non-viable mutant as KILLED. On cobra's `command.go` that was 26 of 274 mutants —
9.5% — counted as evidence the tests were good.

The discriminator is the documented `FailedBuild` field, plus the
`Action:"build-fail"` event:

```json
{"ImportPath":"…[…test]","Action":"build-fail"}
{"Action":"fail","Package":"…","FailedBuild":"example.com/toy/calc [example.com/toy/calc.test]"}
```

### 2. `go test` runs vet by default, and a vet failure looks like a build failure

Take `n >= 0 || n < 0` and mutate `<` to `>=`, a legal `CONDITIONALS_NEGATION`.
The result, `n >= 0 || n >= 0`, is perfectly valid Go — but vet reports
`redundant or`:

```
$ go test -json -overlay=mut.json ./v/
{"Action":"build-fail"} …                     → recorded NOT_VIABLE ✗

$ go test -vet=off -json -overlay=mut.json ./v/
{"Action":"pass",…}                           → SURVIVED ✓
```

Recording it non-viable removes a **genuine survivor** from the denominator and
inflates the score. `-vet=off` is a correctness requirement, not an optimisation.
It is also ~7% faster.

### 3. Concurrency wakes flaky tests, which then read as kills

Cobra's baseline passes five sequential runs in a row. Run it four ways
concurrently — the load the mutant phase creates — and `TestDeadcodeElimination`
fails intermittently, because it shells out to `go build` with a relative path
and races.

The consequence is not theoretical. In `RangeArgs`, mutating `len(args) > max` to
`>= max` is a genuine survivor: 10 out of 10 sequential runs. At four workers it
was reported KILLED in 2 of 3 trials, and the whole result set flipped between
`KILLED=15/SURVIVED=3` and `16/2`.

Outcome-set hashes across trials:

```
with quarantine     w=2 ×3, w=4 ×3  → all six identical, matching the w=1 ground truth
without quarantine  w=4 ×3          → diverged on the third
```

Quarantine also made the run *faster* (1.37s vs 2.22s at two workers) by dropping
a heavyweight test. The counterintuitive conclusion: **quarantine is what makes
parallelism safe — lowering the worker count is not.**

---

## The denominator

metron reports three numbers. The headline, and the gate, counts uncovered
mutants:

```
                   KILLED + TIMED_OUT
  score    = ─────────────────────────────────────────
             KILLED + TIMED_OUT + SURVIVED + NOT_COVERED

  strength = (KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED)
  reach    = 1 − NOT_COVERED / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)
```

**Why NOT_COVERED belongs in the denominator.** metron grades a change, and the
dominant failure in agent-written code is 200 new lines with 20 of them tested
well. Reporting strength alone scores that near 1.0 — a catastrophic false pass,
and one gamed outright by writing a single excellent test for a single tiny
function. Strength measures "are the tests you wrote good tests". The product
question is "is this change held up by tests". Only the second deserves a gate.

**Why NOT_VIABLE stays out.** It is an artifact of metron's own generator, not a
property of the user's tests. Including it makes the score depend on how good the
type gating is, and moves it in the wrong direction: a file with more string
concatenation would grade lower for reasons unrelated to testing. Measured on
`command.go`, including non-viables moves the score from 68.3% to 66.8% as pure
generator noise. It is reported separately, and metron warns about itself when
the rate exceeds 15%.

**TIMED_OUT counts as detected.** An infinite loop introduced by a mutation was
noticed by the suite. Standard in PIT and gremlins.

Strength and reach decompose the headline into *which* failure you are in — low
reach means the code is untested, low strength means the tests execute it and
assert nothing. That decomposition is the actionable output; the single number is
only the gate.

Worked example, cobra `command.go`, 274 mutants after the coverage pre-filter:

```
NOT_COVERED 35   KILLED 182   TIMED_OUT 1   SURVIVED 50   NOT_VIABLE 6
score = 183/268 = 0.683    strength = 183/233 = 0.785    reach = 1−35/268 = 0.869
```

---

## Generation must be type-aware

Go overloads `+` for string concatenation and does not overload `-`, so every
arithmetic mutant landing on a concatenation is **guaranteed** not to compile —
and Go code is full of string concatenation. A purely syntactic generator
produced 274 mutants on `command.go`, 26 of which (9.5%) failed to build.

Adding a `go/types` gate, using the package that is already loaded:

```
candidates=293  emitted=258  SUPPRESSED=35
  ARITHMETIC_BASE        emitted=  4  suppressed= 26   ← 87% of this operator was garbage
  CONDITIONALS_NEGATION  emitted=164  suppressed=  0
  INVERT_LOGICAL         emitted= 53  suppressed=  0
  CONDITIONALS_BOUNDARY  emitted= 25  suppressed=  0
  INVERT_LOOP_CTRL       emitted= 10  suppressed=  4
```

Four suppression layers, cheapest first:

1. **Type gate** — arithmetic and compound assignment require a `*types.Basic`
   operand that is integer, float, or complex.
2. **Constant-operand gate** — identity and degenerate rewrites: `*` or `/` by 1,
   `+` or `-` by 0, `/` by 0.
3. **Label gate** — `break L` becomes `continue L` only when `L` labels a loop.
   Labelling a switch or select makes the mutant a compile error.
4. **Call denylist** — `REMOVE_STATEMENT` skips `log`, `slog`, `fmt.Print*`,
   tracing and metrics calls. Deleting an observability call is semantically
   invisible, so the mutant is unkillable by construction and only depresses the
   score.

**One gate is deliberately absent.** Comparing an unsigned value against zero is
often cited as producing equivalent boundary mutants, since `u >= 0` is a
tautology. It does not: swapping to `u > 0` changes behaviour at zero, and a test
passing zero kills it. Suppressing those would drop real mutants from the
denominator and hide the gap instead of reporting it.

**Byte splicing, not AST re-printing.** Every operator rewrites the exact byte
range and leaves the rest of the file untouched, so the mutated file is
line-for-line identical. That keeps coverage blocks mapping, compiler positions
meaningful, and the report able to show an exact before/after pair.
`REMOVE_STATEMENT` blanks its range to spaces to preserve the line count.

---

## Test selection: the obvious answer is wrong

Cost breakdown on cobra, warm cache, same mutant:

| stage | cumulative | marginal |
| --- | --- | --- |
| `go build -overlay .` | 42 ms | 23 ms |
| `go test -c -overlay .` (compile + **link**) | 223 ms | **181 ms** |
| `go test -overlay . -run=^NOTHING$` (zero tests) | 359 ms | **136 ms** |
| `go test -overlay .` (all 261 tests) | 532 ms | 173 ms |

**The irreducible floor is ~360ms: link plus process start.** Only 173ms of the
532ms is test execution, so perfect test selection can save at most 33%.

Both clever approaches cost more than that:

- **Per-test coverage profiling** is exact, but one
  `-run='^TestX$' -coverprofile` takes 530ms and cobra's root package has 260
  tests — ~138s to learn what to skip, to save ~10s.
- **`codegraph affected`** has no selectivity for Go. Its default glob misses Go
  test conventions and returns nothing; with `-f '*_test.go'` it returns 17 of
  18 test files.

**What actually works is package-scope selection** via the reverse-dependency
closure from `go list -json ./...`. Free, exact, no external index. On a
hundred-package repository that is the difference between a 60-second suite and a
half-second one, which is where essentially all the saving lives.

**Top-level tests only.** `go test -list` cannot enumerate subtests — they are
discovered at run time — and selecting one still runs its parent's body anyway.
Top-level names are Go identifiers, so an alternation needs no escaping. Scale is
not a concern: 261 test names is a 7,690-byte regex against an `ARG_MAX` of
1,048,576. Go's `-run` has no negation, so excluding a quarantined test means
naming the complement explicitly.

---

## The coverage pre-filter

It is effective and **sound**: of the 35 mutants on `command.go` predicted
unreachable, zero were killed when actually run. The filter never discards a
mutant the tests would have caught.

Three edges decide whether it is correct at all:

1. **`-coverpkg=./...` is mandatory.** With a two-package fixture where `core` is
   exercised only by `api`'s tests, the default profile reports **0%** for every
   block in `core`; with `-coverpkg` it reports 100%. Without the flag, every
   mutant in a package whose tests live elsewhere is wrongly written off as
   unreachable — catastrophic on layered code, which is most real Go. Measured
   overhead: none (570ms vs 573ms).
2. **The merged profile contains duplicate blocks with different counts**,
   because it concatenates one profile per test binary. A hand-rolled
   `map[block]count` gets the wrong answer depending on iteration order.
   `golang.org/x/tools/cover.ParseProfiles` merges them correctly. Profile file
   names are import paths, not disk paths.
3. **Case guards belong to no block.** Go instruments a case body, not the case
   expression, so 21 of 274 mutants mapped to nothing. The fallback is the
   enclosing function's aggregate coverage — conservative in the safe direction.
   Never the *following* block: a guard is evaluated even when its branch is not
   taken.

---

## Concurrency, timeouts, determinism

Worker sweep on the toy module, 12 mutants:

| workers | total | mean latency |
| --- | --- | --- |
| 1 | 3.51 s | 293 ms |
| 2 | **1.92 s** | 308 ms |
| 4 | 1.95 s | 574 ms |
| 8 | 2.06 s | 1037 ms |

**Throughput saturates at two workers and degrades past it**, because `go test`
is already internally parallel and one invocation nearly saturates the machine.
metron uses `clamp(NumCPU/4, 2, 4)` with `-p=2 -parallel=n` on the child.

**Per-mutant timeouts are derived, never defaulted.** One runaway mutant at a
fixed 120s timeout consumed 120.8s of a 354s run — 34% of the wall clock. Using
`clamp(4 × baseline duration, 5s, 60s)` gives 5s on cobra instead of 120s, cutting
that run by a third with no loss of information: a timeout is a kill either way.

Sources of nondeterminism and how each is closed:

| source | closed by |
| --- | --- |
| map iteration order | never ranging a map on an output path; candidates sorted by (file, line, col, operator) |
| worker completion order | results written into a pre-sized slice by dispatch index |
| mutant identity | content hash, so ids survive re-runs and budget truncation |
| test result cache | always `-count=1` |
| vet | always `-vet=off` (also a correctness fix) |
| budget truncation | stratified dispatch — round-robin across changed functions, so a truncated run samples all of them rather than exhausting the first |
| flaky tests | quarantine plus adjudication |
| environment | `GOFLAGS`, `TZ`, `LC_ALL`, `-p`, `-parallel` pinned on the child |

The baseline check has four steps: run the unmutated suite N times at the mutant
phase's concurrency; quarantine anything that does not pass every round; abort
the axis if a test fails *every* round, since scoring against a red suite makes
every mutant look killed; and re-run sequentially any mutant whose kill was
credited only to quarantined tests.

---

## Known limits

**Mutant density is ~1 per 7.5 lines of Go** (`args.go`: 140 lines → 18;
`command.go`: 2072 → 274). A 500-line change is ~65 mutants, about 35s
sequentially. A 2000-line change is ~270 mutants and would take minutes, so
`MaxMutants` caps it with stratified sampling and says so.

**Slow suites are refused, not sampled.** Every timing here comes from a package
whose whole suite runs in 173ms. A package with a 30-second suite makes each
mutant cost 30 seconds. When the budget buys under a quarter of the mutants, the
axis reports `n/a` with the arithmetic rather than publishing a number derived
from a handful. A missing value on a lab report is honest; a fabricated one is
worse than having no tool.

**Not implemented:** a persistent equivalence ledger for human-marked equivalent
survivors, and per-test selection for packages slow enough to justify it (the
measurements above say package scope captures nearly all of the win).

**cgo packages are skipped** — `-overlay` has documented limitations with cgo
files included from outside the include path. Generated files are excluded too:
mutating generated code measures the generator, not the author's tests.
