# Measuring code health with metron

metron reports explicit metrics about Go code and, for each gap, the change that
closes it. Run it, read what it found, and turn that into work someone can do in
order.

Every number is deterministic — same commit in, same number out. Never estimate a
metric, and never describe one you did not run. If a reading is unavailable, say
so and say why.

## Check the tools

```bash
command -v metron || echo "install: https://github.com/yanmxa/metron"
command -v go
```

The graph axis needs a CodeGraph index. If `.codegraph/` is absent, ask before
creating one — indexing is the user's decision.

## Measure

After changing Go code, measure the change:

```bash
metron --since main --axes complexity,graph --format json
```

That takes about a second. Add the mutation axis when the question is whether the
tests hold up — it runs the test suite, so it costs minutes on a large repository:

```bash
metron --since main --axes all --budget 10m --format json
```

To review existing code rather than a change, use `--all`. It answers strictly
less: with no base revision there is no `cognitive Δ`, and the bypassed-wrapper
and unprecedented-dependency checks do not run. Those report as unmeasured. Never
present that absence as a pass.

## Read the output

Parse the JSON. Three parts matter:

- `measures` — the readings, each with a `status` of `ok`, `warn`, `fail` or
  `unmeasured`, and the `reference` range it was read against.
- `observations` — the specific findings. **`detail` on a mutation finding is the
  assertion to add.** On a complexity finding it carries the CRAP score.
- `diagnostics` — per-axis numbers with no row of their own, including
  `complexity.crap_max` and the individual graph rule counts.

## Act on it, in this order

1. **Highest CRAP first.** It combines complexity with how well a function is
   pinned by tests, so it answers "which one first" better than either alone. A
   function over 30 that the complexity limit let pass is the most valuable
   finding in the run — say so explicitly.
2. **Then surviving mutants.** Each carries the exact assertion that is missing.
   Write that test, then re-run to confirm the mutant now dies.
3. **Then unreachable and duplicated code.** Cheap to fix, and it shrinks the
   surface everything else is measured over.
4. **Then raw complexity** with no testing signal attached.

Ignore nothing silently. If you deprioritise a finding, say why.

Read the flagged functions before proposing how to restructure them. metron says
a function is hard to read; it does not say what it should become.

## Close the loop

After each change, re-run the same command and show the reading move. Do not
report success because you edited something — verdicts are cached, so re-running
after a small change is fast.

Stop when the exit code is `0`. The codes are the stop condition:

| code | meaning |
| --- | --- |
| `0` | every reading within range |
| `1` | error |
| `2` | a reading fell outside its range |
| `3` | budget spent; the readings cover only a sample, so do not treat as a pass |

**Never edit `metron.json` thresholds, delete tests, or add `//nolint` to make a
reading pass.** A gate is only worth having if the thing being gated cannot move
it. If a reference range is genuinely wrong for the repository, say so and let a
human change it.

## Writing a report

When asked to review rather than fix, structure it as: the headline state, then
what to fix in order, then what was not measured.

```markdown
# Code health: <repo or package>

Measured with `metron --all --axes ...` on <commit>. <N> Go files.

## State

| reading | value | reference | |
|---|---|---|---|
| mutation score | 41% | ≥ 70% | ✗ |
| cognitive max | 53 | ≤ 15 | ✗ |
| cognitive Δ | — | — | not measured (no base revision) |

## Fix in this order

### 1. `internal/foo.go:243` — `generate` · CRAP 91
Cognitive complexity 53 across 91 lines, and 12% of mutants in it are caught.
It is both the hardest function to read and the least pinned, so a change here
is the most likely to break something silently.

Do: extract <the specific responsibilities visible in the code>, then add
assertions for <the specific survivors metron listed>.

## Not measured
- `cognitive Δ` — `--all` has no base revision. Run `metron --since main`.
- graph axis — no `.codegraph` index in this repository.
```

Ground every claim in a number metron produced.

## What each reading means

| reading | out of range means |
| --- | --- |
| `mutation score` | The code is not held up by tests. The gate. |
| `test strength` | Tests run this code but assert too little about it. |
| `reach` | Much of the code is never executed by any test. |
| `cognitive max` | A function is hard to read. Nesting counts more than branching. |
| `cognitive Δ` | A function got worse rather than being extracted. |
| `redundant code` | Something is unreachable, or duplicates what exists. |
| `inconsistent code` | Something bypasses a wrapper, draws an unprecedented dependency, or breaks a local convention. |
| CRAP (per function) | Complexity weighted by how poorly tested it is. Over 30 is the conventional limit. |

Full definitions and worked examples:
https://github.com/yanmxa/metron/blob/main/docs/metrics.md
