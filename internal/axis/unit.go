package axis

import "strconv"

// Unit controls how a reading is rendered. Formatting lives with the value
// because the panel, the JSON writer and the markdown writer must all agree.
type Unit int

const (
	UnitCount Unit = iota
	UnitRatio      // 0..1, printed as a percentage
	UnitDelta      // signed, printed with an explicit + on increases
)

func (u Unit) MarshalJSON() ([]byte, error) {
	return []byte(`"` + u.String() + `"`), nil
}

func (u Unit) String() string {
	switch u {
	case UnitRatio:
		return "ratio"
	case UnitDelta:
		return "delta"
	default:
		return "count"
	}
}

func (u Unit) Format(v float64) string {
	switch u {
	case UnitRatio:
		return strconv.FormatFloat(v*100, 'f', 0, 64) + "%"
	case UnitDelta:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if v > 0 {
			return "+" + s
		}
		return s
	default:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
}

// FormatRange renders a reference range the way a lab report prints one.
func FormatRange(lo, hi *float64, u Unit) string {
	switch {
	case lo != nil && hi != nil:
		if *lo == *hi {
			return "= " + u.Format(*lo)
		}
		return u.Format(*lo) + "–" + u.Format(*hi)
	case lo != nil:
		return "≥ " + u.Format(*lo)
	case hi != nil:
		if *hi == 0 {
			return "= 0"
		}
		return "≤ " + u.Format(*hi)
	default:
		return ""
	}
}
