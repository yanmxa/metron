// Package panel renders the lab report.
//
// Every reading is printed next to the reference range it was read against.
// A number with no range is not actionable, and a single weighted total would
// hide which axis failed — so there isn't one.
package panel

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/target"
)

type Panel struct {
	Target  *target.Target
	Results []*axis.Result
	// ConfigPath, when set, is named in the footer so a surprising reference
	// range can be traced to the file that changed it.
	ConfigPath string
}

// Render produces the terminal form.
func (p *Panel) Render() string {
	var b strings.Builder

	added, removed := 0, 0
	for _, f := range p.Target.Files {
		for _, r := range f.Ranges {
			added += r.End - r.Start + 1
		}
	}
	label := p.Target.BaseRef
	if p.Target.WholeRepo {
		label = p.Target.HeadDesc
	}
	fmt.Fprintf(&b, "\n  METRON  %s · %d files · %d+\n\n", label, len(p.Target.Files), added)
	_ = removed

	rows := p.rows()
	if len(rows) == 0 {
		b.WriteString("  nothing measurable in this change\n")
		return b.String()
	}

	labelW, valueW, rangeW := 4, 4, 8
	for _, r := range rows {
		labelW = max(labelW, width(r.label))
		valueW = max(valueW, width(r.value))
		rangeW = max(rangeW, width(r.ref))
	}

	fmt.Fprintf(&b, "  %s  %s   %s\n", pad("reading", labelW), padLeft("value", valueW), pad("reference", rangeW))
	b.WriteString("  " + strings.Repeat("─", labelW+valueW+rangeW+8) + "\n")
	for _, r := range rows {
		line := fmt.Sprintf("  %s  %s   %s  %s",
			pad(r.label, labelW), padLeft(r.value, valueW), pad(r.ref, rangeW), r.flag)
		if r.note != "" {
			line = pad(line, labelW+valueW+rangeW+12) + "  " + r.note
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}

	b.WriteString(p.footer(rows))
	return b.String()
}

// unmeasuredFlag marks a reading metron could not take. It is neither a pass
// nor a failure — the panel says so rather than letting it look like either.
const unmeasuredFlag = "n/a"

type row struct{ label, value, ref, flag, note string }

func (p *Panel) rows() []row {
	var rows []row
	for _, res := range p.Results {
		for _, m := range res.Measures {
			label := m.Label
			if m.Sub {
				// A breakdown of the reading above it, not a peer.
				label = "  " + label
			}
			if m.Status == axis.StatusUnmeasured {
				rows = append(rows, row{label: label, value: "—", flag: unmeasuredFlag, note: m.Note})
				continue
			}
			rows = append(rows, row{
				label: label,
				value: m.Unit.Format(m.Value),
				ref:   axis.FormatRange(m.RefLow, m.RefHigh, m.Unit),
				flag:  m.Flag(),
				note:  m.Note,
			})
		}
	}
	return rows
}

func (p *Panel) footer(rows []row) string {
	var b strings.Builder
	out, unmeasured := 0, 0
	for _, r := range rows {
		switch r.flag {
		case "H", "L":
			out++
		case unmeasuredFlag:
			unmeasured++
		}
	}

	var notes []string
	for _, res := range p.Results {
		notes = append(notes, res.Notes...)
		if res.Partial {
			notes = append(notes, fmt.Sprintf("the %s axis ran out of budget; its readings cover only a sample", res.AxisID))
		}
	}

	b.WriteString("\n")
	switch {
	case out > 0:
		fmt.Fprintf(&b, "  %d out of range", out)
	default:
		b.WriteString("  all within range")
	}
	if unmeasured > 0 {
		fmt.Fprintf(&b, " · %d unmeasured", unmeasured)
	}
	for _, n := range notes {
		b.WriteString(" · " + n)
	}
	if p.ConfigPath != "" {
		fmt.Fprintf(&b, " · ranges from %s", filepath.Base(p.ConfigPath))
	}
	b.WriteString("\n")

	// The observations are what make a reading actionable.
	for _, res := range p.Results {
		if len(res.Observations) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n  %s\n", res.AxisID)
		for _, o := range res.Observations {
			fmt.Fprintf(&b, "    %s:%d  %s\n", o.Path, o.Line, o.Title)
			// A surviving mutant is only actionable if you can see the edit
			// the tests failed to notice.
			if o.Before != "" && o.After != "" {
				fmt.Fprintf(&b, "      - %s\n      + %s\n", o.Before, o.After)
			}
			if o.Detail != "" {
				fmt.Fprintf(&b, "      %s\n", o.Detail)
			}
		}
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
