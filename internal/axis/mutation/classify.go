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
	var (
		buildFail   bool
		buildOutput []string
		pkgFailed   bool
		pkgPassed   bool
		timeout     bool
		timeoutMsg  string
		failedTests = map[string]bool{}
		started     = map[string]bool{}
		finished    = map[string]bool{}
	)

	for _, e := range events {
		switch e.Action {
		case "build-fail":
			buildFail = true
		case "build-output":
			if s := strings.TrimSpace(e.Output); s != "" {
				buildOutput = append(buildOutput, s)
			}
		case "run":
			if e.Test != "" {
				started[e.Test] = true
			}
		case "pass":
			if e.Test == "" {
				pkgPassed = true
			} else {
				finished[e.Test] = true
			}
		case "skip":
			if e.Test != "" {
				finished[e.Test] = true
			}
		case "fail":
			if e.Test == "" {
				pkgFailed = true
				if e.FailedBuild != "" {
					buildFail = true
				}
			} else {
				finished[e.Test] = true
				failedTests[e.Test] = true
			}
		case "output":
			if strings.Contains(e.Output, "test timed out after") ||
				strings.Contains(e.Output, "*** Test killed") {
				timeout = true
				timeoutMsg = strings.TrimSpace(e.Output)
			}
		}
	}

	// A test that started and never finished means the binary was killed
	// mid-run — the other face of a timeout.
	dangling := false
	for name := range started {
		if !finished[name] {
			dangling = true
			break
		}
	}

	switch {
	case buildFail:
		return Verdict{Outcome: NotViable, Detail: firstLine(buildOutput)}
	case timeout || (pkgFailed && dangling):
		return Verdict{Outcome: TimedOut, Detail: timeoutMsg}
	case pkgFailed:
		return Verdict{Outcome: Killed, KilledBy: sortedKeys(failedTests)}
	case pkgPassed:
		return Verdict{Outcome: Survived}
	case runErr != nil:
		// Non-JSON noise on stderr, a missing toolchain, a killed process:
		// anything unexplained is an error, never a kill.
		return Verdict{Outcome: Errored, Detail: runErr.Error()}
	default:
		return Verdict{Outcome: Errored, Detail: "no terminal event in the test output"}
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
