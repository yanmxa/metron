---
name: code-health
description: Measure existing Go code with metron — mutation score, cognitive complexity, and graph-level redundancy — then write a prioritised report of what to fix and why. Use when asked to review code health, find untested or over-complex code, assess a codebase before changing it, or check whether tests actually hold the code up. Also use after generating Go code, to verify it rather than assume it.
---

# Measuring code health with metron

metron reports explicit metrics about Go code and, for each gap, the assertion
or change that closes it. Your job is to run it, read what it found, and turn
that into a report someone can act on in order.

Every number is deterministic. Do not estimate, guess, or describe a metric you
did not run — if a reading is unavailable, say so and say why.

## 1. Check the tools

```bash
command -v metron || go install github.com/yanmxa/metron/cmd/metron@latest
command -v go
```

The graph axis needs a CodeGraph index. If `.codegraph/` is absent, ask before
creating one — indexing is the user's decision:

```bash
ls -d .codegraph 2>/dev/null || echo "no index; graph axis will report n/a"
```

## 2. Measure

For existing code, whole repository:

```bash
metron --all --axes complexity,graph --format json
```

That takes about a second. Then, only if the user wants the mutation axis (it
runs their test suite and takes minutes on a large repo):

```bash
metron --all --axes all --budget 10m --format json
```

For a change rather than a whole repository, use `--since <ref>` instead of
`--all`. Prefer that when the user is asking about work in progress.

**What `--all` cannot answer.** With no base revision there is no
`cognitive Δ`, and bypassed-wrapper and unprecedented-dependency checks do not
run. Those report as unmeasured. Do not present the absence as a pass.

## 3. Read the output

Parse the JSON. Three parts matter:

- `measures` — the readings and whether each is in range. `status` is one of
  `ok`, `warn`, `fail`, `unmeasured`.
- `observations` — the specific findings. `detail` on a mutation finding is the
  assertion to add; on a complexity finding it carries the CRAP score.
- `diagnostics` — per-axis numbers with no row of their own, including
  `complexity.crap_max` and the individual graph rule counts.

## 4. Prioritise

Rank by what actually costs the reader, not by the order metron printed:

1. **Highest CRAP first.** It combines complexity with how well the function is
   pinned by tests, so it answers "which one first" better than either alone. A
   function over 30 that complexity alone let pass is the most valuable finding
   in the run — say so explicitly.
2. **Then surviving mutants in code that matters.** Each carries the exact
   assertion that is missing.
3. **Then unreachable and duplicated code.** Cheap to fix, and it shrinks the
   surface everything else is measured over.
4. **Then raw complexity** with no testing signal attached.

Ignore nothing silently. If you deprioritise a finding, say why.

## 5. Write the report

Structure it as: the headline state, then what to fix in order, then what was
not measured.

```markdown
# Code health: <repo or package>

Measured with `metron --all --axes ...` on <commit>. <N> Go files.

## State

| reading | value | reference | |
|---|---|---|---|
| mutation score | 41% | ≥ 70% | ✗ |
| cognitive max | 53 | ≤ 15 | ✗ |
| redundant code | 3 | = 0 | ✗ |
| cognitive Δ | — | — | not measured (no base revision) |

## Fix in this order

### 1. `internal/axis/mutation/axis.go:243` — `generate`  · CRAP 91
Cognitive complexity 53 across 91 lines, and only 12% of mutants in it are
caught. It is both the hardest function to read and the least pinned, so a
change here is the most likely to break something silently.

Do: extract <the specific responsibilities you can see in the code>, then add
assertions for <the specific survivors metron listed>.

### 2. ...

## Not measured
- `cognitive Δ` — `--all` has no base revision. Run `metron --since main` on a
  branch to get it.
- graph axis — no `.codegraph` index in this repository.
```

Ground every claim in a number metron produced. Read the flagged functions
before recommending how to restructure them: metron says a function is hard to
read, it does not say what it should become.

## 6. If asked to fix, close the loop

After each change, re-run the same command and show the reading move. Do not
report success on the basis of having edited something — mutation verdicts are
cached, so re-running after a small change is fast.

**Never edit thresholds or delete tests to make a reading pass.** If a reference
range is genuinely wrong for this repository, say so and let the user change it.

## Reading the metrics

Full definitions, worked examples and what each metric is for:
[docs/metrics.md](../../../docs/metrics.md) in this repository, or the README.

| reading | what it means when out of range |
| --- | --- |
| `mutation score` | The code is not held up by tests. The gate. |
| `test strength` | Tests run this code but assert too little about it. |
| `reach` | Much of the code is never executed by any test. |
| `cognitive max` | A function is hard to read. Nesting counts more than branching. |
| `cognitive Δ` | A function got worse rather than being extracted. |
| `redundant code` | Something is unreachable, or duplicates what exists. |
| `inconsistent code` | Something bypasses a wrapper, draws an unprecedented dependency, or breaks a local convention. |
| CRAP (per function) | Complexity weighted by how poorly tested it is. Over 30 is the conventional limit. |
