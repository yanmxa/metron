package panel

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yanmxa/metron/internal/axis"
)

func TestProgressStaysSilentWhenDisabled(t *testing.T) {
	// Redraws in a CI log or a pipe are noise, and they would corrupt
	// --format json if they went to stdout.
	var b bytes.Buffer
	p := NewProgress(&b, false)
	p.show(axis.Progress{AxisID: "mutation", Stage: "execute", Done: 3, Total: 9})
	p.Clear()
	if b.Len() != 0 {
		t.Errorf("wrote %q while disabled", b.String())
	}
}

func TestProgressShowsWhatIsHappeningAndHowFar(t *testing.T) {
	var b bytes.Buffer
	p := NewProgress(&b, true)
	p.show(axis.Progress{AxisID: "mutation", Stage: "execute", Done: 3, Total: 9})

	got := b.String()
	for _, want := range []string{"mutation", "execute", "3/9"} {
		if !strings.Contains(got, want) {
			t.Errorf("progress line %q is missing %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "\r") {
		t.Error("should redraw in place rather than scrolling")
	}
}

func TestProgressErasesALongerPreviousLine(t *testing.T) {
	// Going from a long stage name to a short one must not leave the tail of
	// the old line on screen.
	var b bytes.Buffer
	p := NewProgress(&b, true)
	p.show(axis.Progress{AxisID: "mutation", Stage: "execute", Done: 100, Total: 100,
		Message: "a rather long message about what is happening"})
	b.Reset()
	p.show(axis.Progress{AxisID: "graph", Stage: "load"})

	got := b.String()
	if !strings.HasSuffix(got, "  ") {
		t.Errorf("short line %q should be padded to erase the longer one", got)
	}
}

func TestClearLeavesTheCursorOnACleanLine(t *testing.T) {
	var b bytes.Buffer
	p := NewProgress(&b, true)
	p.show(axis.Progress{AxisID: "mutation", Stage: "baseline"})
	b.Reset()
	p.Clear()
	if got := b.String(); !strings.HasSuffix(got, "\r") || strings.TrimSpace(got) != "" {
		t.Errorf("Clear wrote %q, want only blanking", got)
	}
}
