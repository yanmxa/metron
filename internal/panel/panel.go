// Package panel renders the lab report.
//
// Every reading is printed next to the reference range it was read against.
// A number with no range is not actionable, and a single weighted total would
// hide which axis failed — so there isn't one.
package panel

import (
	"fmt"
	"strings"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/target"
)

type Panel struct {
	Target  *target.Target
	Results []*axis.Result
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
	fmt.Fprintf(&b, "\n  METRON  %s · %d files · %d+\n\n",
		p.Target.BaseRef, len(p.Target.Files), added)
	_ = removed

	rows := p.rows()
	if len(rows) == 0 {
		b.WriteString("  没有可测的改动\n")
		return b.String()
	}

	labelW, valueW, rangeW := 4, 4, 8
	for _, r := range rows {
		labelW = max(labelW, width(r.label))
		valueW = max(valueW, width(r.value))
		rangeW = max(rangeW, width(r.ref))
	}

	fmt.Fprintf(&b, "  %s  %s   %s\n", pad("指标", labelW), padLeft("读数", valueW), pad("参考区间", rangeW))
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

type row struct{ label, value, ref, flag, note string }

func (p *Panel) rows() []row {
	var rows []row
	for _, res := range p.Results {
		for _, m := range res.Measures {
			if m.Status == axis.StatusUnmeasured {
				rows = append(rows, row{label: m.Label, value: "—", flag: "未测", note: m.Note})
				continue
			}
			rows = append(rows, row{
				label: m.Label,
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
		case "未测":
			unmeasured++
		}
	}

	var notes []string
	for _, res := range p.Results {
		notes = append(notes, res.Notes...)
		if res.Partial {
			notes = append(notes, fmt.Sprintf("%s 轴预算耗尽,读数只覆盖了一部分", res.AxisID))
		}
	}

	b.WriteString("\n")
	switch {
	case out > 0:
		fmt.Fprintf(&b, "  %d 项超出区间", out)
	default:
		b.WriteString("  全部在区间内")
	}
	if unmeasured > 0 {
		fmt.Fprintf(&b, " · %d 项未测", unmeasured)
	}
	for _, n := range notes {
		b.WriteString(" · " + n)
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
