// Package gate turns readings into an exit code.
package gate

import "github.com/yanmxa/metron/internal/axis"

type Policy string

const (
	// FailOnHeadline gates on the headline readings only. Diagnostics inform;
	// they do not block.
	FailOnHeadline Policy = "headline"
	// FailOnAny gates on every reading with a reference range.
	FailOnAny Policy = "any"
	// FailOnNone measures without blocking.
	FailOnNone Policy = "none"
)

const (
	ExitOK         = 0
	ExitError      = 1
	ExitOutOfRange = 2
	ExitPartial    = 3
)

// Decide returns the process exit code.
//
// A partial run cannot fail a build: the sample is not the population, and a
// tool that fails on incomplete evidence teaches people to ignore it.
func Decide(results []*axis.Result, p Policy) int {
	partial := false
	outOfRange := false

	for _, r := range results {
		if r.Partial {
			partial = true
		}
		for _, m := range r.Measures {
			if m.Status == axis.StatusUnmeasured {
				continue
			}
			if p == FailOnHeadline && !m.Headline {
				continue
			}
			if !m.InRange() {
				outOfRange = true
			}
		}
	}

	switch {
	case partial:
		return ExitPartial
	case p == FailOnNone:
		return ExitOK
	case outOfRange:
		return ExitOutOfRange
	default:
		return ExitOK
	}
}
