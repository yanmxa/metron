# Contributing

Thanks for looking. This document is short because most of the rules are
enforced by CI rather than by review.

## Getting set up

```bash
git clone https://github.com/yanmxa/metron
cd metron
make check      # gofmt, vet, test, and metron on itself
```

Go 1.26 or newer. No other tooling is required to build or test.

## What CI enforces

| check | how to run it locally |
| --- | --- |
| formatting | `gofmt -l .` must print nothing |
| vet | `go vet ./...` |
| tests | `go test -race ./...` |
| lint | `golangci-lint run` |
| metron on itself | `make dogfood` |

## The complexity ratchet

metron measures itself, and the limit in [`metron.json`](metron.json) sits at
this repository's current worst function rather than at an aspirational number.

Two rules follow from that:

- **`maxDelta` is 0 and stays 0.** No existing function may get worse. This is
  what makes the raised limit a ratchet instead of a surrender.
- **`maxCognitive` only ever goes down.** When you split up the worst function,
  lower it to the new worst in the same pull request. Never raise it to make a
  change pass — split the function instead.

If you genuinely believe a range is wrong for this repository, say so in the
pull request and argue it. Do not change it quietly.

## Tests

Test names are sentences that state the invariant, not the method under test:

```go
func TestBuildFailureIsNotAKill(t *testing.T)
func TestGuidanceNeverClaimsWhatTheTestsDo(t *testing.T)
func TestUncoveredMutantsCountAgainstTheScore(t *testing.T)
```

When a test exists because something went wrong once, say so in a comment. Most
of the sharp edges in this codebase were found by measurement, and the comment
is what stops them being smoothed back out:

```go
// The trap: a mutant that does not compile emits a package-level
// {"Action":"fail"} identical to a real test failure. Reading the exit code
// scores every non-viable mutant as KILLED and inflates the score.
```

## Adding a mutation operator

An operator must do three things, or it will make the score less trustworthy
rather than more:

1. **Be type-aware.** Go overloads `+` for strings and does not overload `-`, so
   an arithmetic mutant on a concatenation cannot compile. Gate on `go/types`.
2. **Preserve line count.** Every operator is a byte splice, never an AST
   re-print, so coverage blocks still map and compiler positions still mean
   something.
3. **Say what its survival means.** Add a case to `guide()` phrased as *the
   assertion to add*, never as a claim about what the tests currently do.

See [docs/mutation-design.md](docs/mutation-design.md) for the measurements
behind the existing operators.

## Commit messages

Say what changed and why it needed to. If a number or a measurement drove the
change, put it in the message — it is the part that will still be useful in a
year.
