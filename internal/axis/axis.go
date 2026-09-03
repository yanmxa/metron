// Package axis defines the vocabulary every measurement shares: a reading, the
// reference range it is read against, and whether it falls outside.
//
// The lab-report framing is load-bearing. A reading with no reference range is
// a number nobody can act on, and a reading metron could not take must say so
// rather than quietly default to something that looks like a pass.
package axis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yanmxa/metron/internal/target"
)

type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusFail
	// StatusUnmeasured is first-class on purpose. A missing value on a lab
	// report is honest; a fabricated one is worse than having no tool.
	StatusUnmeasured
)

func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unmeasured"
	}
}

// MarshalJSON fills the flag so machine consumers see the same verdict the
// panel prints, without reimplementing the range comparison.
func (m Measure) MarshalJSON() ([]byte, error) {
	type alias Measure // avoid recursing into this method
	m.Flagged = m.Flag()
	return json.Marshal(alias(m))
}

// Flag is the lab-report marker: H above the range, L below, ✓ inside. A
// measure with no reference range gets no mark — it is a diagnostic, and
// stamping it ✓ would read as a pass it never earned.
func (m Measure) Flag() string {
	switch {
	case m.Status == StatusUnmeasured:
		return "—"
	case m.RefLow == nil && m.RefHigh == nil:
		return ""
	case m.RefHigh != nil && m.Value > *m.RefHigh:
		return "H"
	case m.RefLow != nil && m.Value < *m.RefLow:
		return "L"
	default:
		return "✓"
	}
}

// Measure is one reading.
type Measure struct {
	Key      string   `json:"key"`   // stable id, e.g. "mutation.score"
	Label    string   `json:"label"` // what the panel prints
	Value    float64  `json:"value"`
	Unit     Unit     `json:"unit"`
	RefLow   *float64 `json:"refLow,omitempty"`
	RefHigh  *float64 `json:"refHigh,omitempty"`
	Status   Status   `json:"status"`
	Headline bool     `json:"headline"` // headline readings gate; the rest are diagnostics
	Note     string   `json:"note,omitempty"`
	Flagged  string   `json:"flag"` // H / L / ✓, filled at marshal time
}

// InRange reports whether the reading sits inside its reference range. A
// measure with no range is always in range — it is a diagnostic, not a gate.
func (m Measure) InRange() bool {
	if m.RefLow != nil && m.Value < *m.RefLow {
		return false
	}
	if m.RefHigh != nil && m.Value > *m.RefHigh {
		return false
	}
	return true
}

// Observation is one concrete thing behind a reading — a surviving mutant, an
// orphaned symbol, a function that got harder to read. This is what makes a
// number actionable, so every axis emits them.
type Observation struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	Kind    string `json:"kind"` // "survived-mutant", "orphan-symbol", ...
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	Before  string `json:"before,omitempty"` // for mutation: the original source line
	After   string `json:"after,omitempty"`  // for mutation: the mutated source line
}

// Result is one axis's contribution to the panel.
type Result struct {
	AxisID       string        `json:"axis"`
	Measures     []Measure     `json:"measures"`
	Observations []Observation `json:"observations,omitempty"`
	Notes        []string      `json:"notes,omitempty"` // e.g. "quarantined 1 flaky test"
	Partial      bool          `json:"partial"`         // budget ran out; readings describe a sample
	Duration     time.Duration `json:"durationMs"`
}

// Headlines returns the measures that gate.
func (r *Result) Headlines() []Measure {
	var out []Measure
	for _, m := range r.Measures {
		if m.Headline {
			out = append(out, m)
		}
	}
	return out
}

type Progress struct {
	AxisID  string
	Stage   string
	Done    int
	Total   int
	Message string
}

// Axis is one measurement subsystem.
type Axis interface {
	ID() string
	// Available reports whether this axis can run here, and if not, says why in
	// terms the user can act on ("no .codegraph index — run codegraph init").
	// An axis that cannot run must never be silently treated as a pass.
	Available(ctx context.Context, t *target.Target) (bool, string)
	Run(ctx context.Context, t *target.Target, prog chan<- Progress) (*Result, error)
}

func Ref(v float64) *float64 { return &v }
