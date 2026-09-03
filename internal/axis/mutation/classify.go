package mutation

import (
	"encoding/json"
	"sort"
	"strings"
)

// Outcome is what happened to one mutant.
type Outcome string

const (
	Killed     Outcome = "KILLED"      // at least one selected test failed
	Survived   Outcome = "SURVIVED"    // every selected test passed
	TimedOut   Outcome = "TIMED_OUT"   // exceeded the derived deadline
	NotCovered Outcome = "NOT_COVERED" // pre-filtered; never executed
	NotViable  Outcome = "NOT_VIABLE"  // did not compile
	Skipped    Outcome = "SKIPPED"     // budget ran out before it was reached
	Errored    Outcome = "ERRORED"     // the harness itself failed
)

// Event is one line of `go test -json`. Build events and test events share the
// stream; FailedBuild is what separates them at the package level.
type Event struct {
	Action      string  `json:"Action"`
	Package     string  `json:"Package"`
	Test        string  `json:"Test"`
	Elapsed     float64 `json:"Elapsed"`
	Output      string  `json:"Output"`
	FailedBuild string  `json:"FailedBuild"`
	ImportPath  string  `json:"ImportPath"`
}

// Verdict is the classified result of one mutant run.
type Verdict struct {
	Outcome  Outcome
	KilledBy []string // the tests that failed; required to adjudicate false kills
	Detail   string   // compiler diagnostic or timeout message
}

// Classify turns a `go test -json` event stream into a verdict.
//
// This is the highest-risk correctness surface in the subsystem, and it is a
// pure function so it can be tested without running anything.
//
// The trap it exists to avoid: a mutant that does not compile exits 1 and
// emits a package-level {"Action":"fail"} byte-for-byte identical to a real
// test failure. Classifying on the exit code, or on "the package failed",
// scores every non-viable mutant as KILLED — on one real file that was 26 of
// 274 mutants counted as evidence the tests were good. `FailedBuild` and
// `Action:"build-fail"` are the documented discriminators.
//
// A timeout has its own signature: a test starts and never reaches a terminal
// event, and the package failure carries no FailedBuild.
func Classify(events []Event, runErr error) Verdict {
	st := scan(events)

	switch {
	case st.buildFailed:
		return Verdict{Outcome: NotViable, Detail: firstLine(st.buildOutput)}
	case st.timedOut || (st.pkgFailed && st.hasDanglingTest()):
		return Verdict{Outcome: TimedOut, Detail: st.timeoutMsg}
	case st.pkgFailed:
		return Verdict{Outcome: Killed, KilledBy: sortedKeys(st.failedTests)}
	case st.pkgPassed:
		return Verdict{Outcome: Survived}
	case runErr != nil:
		// Non-JSON noise on stderr, a missing toolchain, a killed process:
		// anything unexplained is an error, never a kill.
		return Verdict{Outcome: Errored, Detail: runErr.Error()}
	default:
		return Verdict{Outcome: Errored, Detail: "no terminal event in the test output"}
	}
}

// state is what one run's event stream amounts to.
type state struct {
	buildFailed bool
	buildOutput []string
	pkgFailed   bool
	pkgPassed   bool
	timedOut    bool
	timeoutMsg  string
	failedTests map[string]bool
	started     map[string]bool
	finished    map[string]bool
}

// hasDanglingTest reports a test that began and never reached a terminal event,
// which is what a binary killed mid-run leaves behind.
func (s *state) hasDanglingTest() bool {
	for name := range s.started {
		if !s.finished[name] {
			return true
		}
	}
	return false
}

func scan(events []Event) *state {
	st := &state{
		failedTests: map[string]bool{},
		started:     map[string]bool{},
		finished:    map[string]bool{},
	}
	for _, e := range events {
		st.apply(e)
	}
	return st
}

func (s *state) apply(e Event) {
	switch e.Action {
	case "build-fail":
		s.buildFailed = true
	case "build-output":
		if t := strings.TrimSpace(e.Output); t != "" {
			s.buildOutput = append(s.buildOutput, t)
		}
	case "run":
		s.mark(e, s.started)
	case "pass":
		s.terminal(e, &s.pkgPassed)
	case "skip":
		s.mark(e, s.finished)
	case "fail":
		s.fail(e)
	case "output":
		s.maybeTimeout(e)
	}
}

func (s *state) mark(e Event, set map[string]bool) {
	if e.Test != "" {
		set[e.Test] = true
	}
}

// terminal records a package-level outcome, or finishes one test.
func (s *state) terminal(e Event, pkgFlag *bool) {
	if e.Test == "" {
		*pkgFlag = true
		return
	}
	s.finished[e.Test] = true
}

func (s *state) fail(e Event) {
	s.terminal(e, &s.pkgFailed)
	if e.Test == "" {
		// A build failure exits 1 and emits a package-level fail identical to a
		// real test failure. FailedBuild is what tells them apart.
		if e.FailedBuild != "" {
			s.buildFailed = true
		}
		return
	}
	s.failedTests[e.Test] = true
}

func (s *state) maybeTimeout(e Event) {
	if strings.Contains(e.Output, "test timed out after") ||
		strings.Contains(e.Output, "*** Test killed") {
		s.timedOut = true
		s.timeoutMsg = strings.TrimSpace(e.Output)
	}
}

// ParseEvents decodes a `go test -json` stream, skipping any non-JSON lines.
// The go command can still write plain text to stderr for early failures, and
// that text must never be mistaken for a result.
func ParseEvents(r []byte) []Event {
	var out []Event
	for _, line := range strings.Split(string(r), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			out = append(out, e)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstLine(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return strings.SplitN(ss[0], "\n", 2)[0]
}
