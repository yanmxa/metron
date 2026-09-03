package panel

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/yanmxa/metron/internal/axis"
)

// Progress renders what an axis is doing while it does it.
//
// The mutation axis can run for minutes. Without this it produced no output at
// all until it finished, which is indistinguishable from being hung — the most
// common reason people kill a tool that was about to succeed.
//
// It writes to stderr so that --format json stays pipeable, and it is silent
// when stderr is not a terminal so CI logs do not fill with redraws.
type Progress struct {
	w       io.Writer
	enabled bool

	mu      sync.Mutex
	last    string
	started time.Time
}

func NewProgress(w io.Writer, enabled bool) *Progress {
	return &Progress{w: w, enabled: enabled, started: time.Now()}
}

// Watch consumes an axis's progress events until the channel closes.
func (p *Progress) Watch(ch <-chan axis.Progress) {
	for ev := range ch {
		p.show(ev)
	}
}

func (p *Progress) show(ev axis.Progress) {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	line := fmt.Sprintf("  %s · %s", ev.AxisID, ev.Stage)
	if ev.Total > 0 {
		line += fmt.Sprintf(" %d/%d", ev.Done, ev.Total)
	}
	if ev.Message != "" {
		line += " · " + ev.Message
	}
	if d := time.Since(p.started).Truncate(time.Second); d > 0 {
		line += fmt.Sprintf("  (%s)", d)
	}
	p.write(line)
}

func (p *Progress) write(line string) {
	pad := len(p.last) - len(line)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(p.w, "\r%s%s", line, strings.Repeat(" ", pad))
	p.last = line
}

// Clear erases the progress line so the report starts on a clean row.
func (p *Progress) Clear() {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last != "" {
		fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", len(p.last)))
		p.last = ""
	}
}
