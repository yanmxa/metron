package axis

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlagMarksWhichSideOfTheRangeAValueFellOn(t *testing.T) {
	hi, lo := 10.0, 2.0
	tests := []struct {
		name string
		m    Measure
		want string
	}{
		{"above the ceiling", Measure{Value: 11, RefHigh: &hi}, "H"},
		{"below the floor", Measure{Value: 1, RefLow: &lo}, "L"},
		{"inside", Measure{Value: 5, RefLow: &lo, RefHigh: &hi}, "✓"},
		{"on the ceiling is inside", Measure{Value: 10, RefHigh: &hi}, "✓"},
		{"unmeasured", Measure{Status: StatusUnmeasured}, "—"},
		// A diagnostic has no range to be inside, and stamping it ✓ would read
		// as a pass it never earned.
		{"no range at all", Measure{Value: 99}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.Flag(); got != tc.want {
				t.Errorf("Flag() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInRangeTreatsAMissingBoundAsUnbounded(t *testing.T) {
	hi := 10.0
	if !(Measure{Value: -999, RefHigh: &hi}).InRange() {
		t.Error("no floor means nothing is too low")
	}
	if (Measure{Value: 11, RefHigh: &hi}).InRange() {
		t.Error("the ceiling should still apply")
	}
	if !(Measure{Value: 12345}).InRange() {
		t.Error("a measure with no range is a diagnostic and always in range")
	}
}

func TestUnmeasuredIsNeitherPassNorFail(t *testing.T) {
	// It must not read as a pass. That is the whole reason the state exists.
	m := Measure{Status: StatusUnmeasured, Note: "no base revision"}
	if m.Flag() == "✓" {
		t.Error("unmeasured must not be flagged as in range")
	}
	if m.Status.String() != "unmeasured" {
		t.Errorf("String() = %q", m.Status.String())
	}
}

func TestJSONCarriesTheVerdictAndReadableEnums(t *testing.T) {
	// A consumer should never have to know that status 2 means fail, and should
	// not have to reimplement the range comparison to learn the verdict.
	hi := 10.0
	buf, err := json.Marshal(Measure{
		Key: "x.y", Label: "x", Value: 11, Unit: UnitRatio,
		RefHigh: &hi, Status: StatusFail,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf)
	for _, want := range []string{`"status":"fail"`, `"unit":"ratio"`, `"flag":"H"`, `"key":"x.y"`} {
		if !strings.Contains(got, want) {
			t.Errorf("json %s is missing %s", got, want)
		}
	}
}

func TestHeadlinesAreWhatGates(t *testing.T) {
	r := &Result{Measures: []Measure{
		{Key: "a", Headline: true}, {Key: "b"}, {Key: "c", Headline: true},
	}}
	got := r.Headlines()
	if len(got) != 2 || got[0].Key != "a" || got[1].Key != "c" {
		t.Errorf("Headlines() = %v", got)
	}
}

func TestUnitFormatting(t *testing.T) {
	tests := []struct {
		unit Unit
		v    float64
		want string
	}{
		{UnitRatio, 0.683, "68%"},
		{UnitRatio, 1, "100%"},
		{UnitCount, 12, "12"},
		{UnitDelta, 9, "+9"}, // an increase needs its sign to read as movement
		{UnitDelta, -3, "-3"},
		{UnitDelta, 0, "0"},
	}
	for _, tc := range tests {
		if got := tc.unit.Format(tc.v); got != tc.want {
			t.Errorf("%v.Format(%v) = %q, want %q", tc.unit, tc.v, got, tc.want)
		}
	}
}

func TestFormatRangeReadsLikeALabReport(t *testing.T) {
	lo, hi, zero := 0.7, 15.0, 0.0
	tests := []struct {
		name   string
		lo, hi *float64
		unit   Unit
		want   string
	}{
		{"floor only", &lo, nil, UnitRatio, "≥ 70%"},
		{"ceiling only", nil, &hi, UnitCount, "≤ 15"},
		{"a ceiling of zero reads as an equality", nil, &zero, UnitCount, "= 0"},
		{"both", &lo, &lo, UnitRatio, "= 70%"},
		{"neither", nil, nil, UnitCount, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRange(tc.lo, tc.hi, tc.unit); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPerFuncKeyIdentifiesAFunctionAcrossAxes(t *testing.T) {
	// Two axes measure different things about the same function; the key is
	// what lets those be combined.
	a := PerFunc{Path: "a.go", Function: "(T).Do", Cyclomatic: 4}
	b := PerFunc{Path: "a.go", Function: "(T).Do", Mutants: 10}
	if a.Key() != b.Key() {
		t.Errorf("%q != %q", a.Key(), b.Key())
	}
	if (PerFunc{Path: "a.go", Function: "Do"}).Key() == a.Key() {
		t.Error("a method and a function of the same name must not collide")
	}
}
