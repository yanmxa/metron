# Changelog

Notable changes. This project follows [semantic versioning](https://semver.org).

## v0.1.0 — unreleased

First tagged release.

### Measures

- **mutation** — rewrites the changed code in small deliberate ways and runs the
  tests against each rewrite. Reports `score`, and decomposes it into `strength`
  (do the tests that run assert anything) and `reach` (do they run it at all).
  Uncovered mutants count against the score; mutants that fail to compile do not.
- **complexity** — cognitive complexity per the SonarSource specification over
  `go/ast`, plus the delta against the merge base. Go's canonical error guards
  are discounted, since counting them in full makes every Go function look
  complex.
- **graph** — unreachable and duplicated code, and code that bypasses a wrapper,
  draws an unprecedented dependency, or breaks a local convention. Read from a
  CodeGraph index.
- **CRAP** — cyclomatic complexity weighted by how poorly a function is pinned by
  tests, ranking findings rather than gating. Uses the per-function mutation
  score in place of Crap4j's line coverage.

### Guides rather than grades

- Every surviving mutant carries the assertion it proves is missing, derived from
  the operator and its operands. `--format json` exposes it as `detail`.
- A `code-health` skill runs the whole-repository analysis and writes a
  prioritised report.

### Behaviour

- `--all` measures a whole repository. Readings needing a base revision report as
  unmeasured rather than as comfortable zeros.
- `metron init` writes a config calibrated to the repository's current worst
  function, so the first run passes and later ones are signal.
- Verdicts are content-addressed and cached; an interrupted run resumes.
- A partial run never fails a build, and a suite too slow to sample is refused
  rather than estimated.
- Progress is reported on stderr for runs that take minutes.

### Fixed before release

- Untracked Go files were invisible to the diff, so a change made entirely of new
  files measured as nothing changed and reported all clear.
- A data race in the mutant planner: every worker shared an unguarded map of file
  contents, which corrupts silently rather than failing loudly.
