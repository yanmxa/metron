package mutation

import (
	"errors"
	"testing"
)

func TestBuildFailureIsNotAKill(t *testing.T) {
	// The trap: a mutant that does not compile emits a package-level
	// {"Action":"fail"} identical to a real test failure. Reading the exit code
	// scores every non-viable mutant as KILLED and inflates the score.
	events := ParseEvents([]byte(`
{"ImportPath":"example.com/p [example.com/p.test]","Action":"build-output","Output":"./m.go:16:7: invalid operation: t + xs (mismatched types int and []int)\n"}
{"ImportPath":"example.com/p [example.com/p.test]","Action":"build-fail"}
{"Action":"fail","Package":"example.com/p","Elapsed":0,"FailedBuild":"example.com/p [example.com/p.test]"}
`))
	got := Classify(events, errors.New("exit status 1"))
	if got.Outcome != NotViable {
		t.Fatalf("outcome = %s, want %s", got.Outcome, NotViable)
	}
	if got.Detail == "" {
		t.Error("the compiler diagnostic should be kept")
	}
}

func TestFailedBuildFieldAloneIsEnough(t *testing.T) {
	// Some streams carry FailedBuild on the package event without a separate
	// build-fail action.
	events := ParseEvents([]byte(
		`{"Action":"fail","Package":"example.com/p","FailedBuild":"example.com/p [example.com/p.test]"}`))
	if got := Classify(events, errors.New("exit 1")); got.Outcome != NotViable {
		t.Errorf("outcome = %s, want %s", got.Outcome, NotViable)
	}
}

func TestRealTestFailureIsAKillAndNamesTheTests(t *testing.T) {
	events := ParseEvents([]byte(`
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"fail","Package":"p","Test":"TestA","Elapsed":0.01}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"pass","Package":"p","Test":"TestB","Elapsed":0.01}
{"Action":"fail","Package":"p","Elapsed":0.4}
`))
	got := Classify(events, errors.New("exit status 1"))
	if got.Outcome != Killed {
		t.Fatalf("outcome = %s, want %s", got.Outcome, Killed)
	}
	if len(got.KilledBy) != 1 || got.KilledBy[0] != "TestA" {
		t.Errorf("killedBy = %v, want [TestA] — adjudicating false kills needs this", got.KilledBy)
	}
}

func TestAllPassingIsSurvival(t *testing.T) {
	events := ParseEvents([]byte(`
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.01}
{"Action":"pass","Package":"p","Elapsed":0.4}
`))
	if got := Classify(events, nil); got.Outcome != Survived {
		t.Errorf("outcome = %s, want %s", got.Outcome, Survived)
	}
}

func TestTimeoutIsDistinguishedFromAFailure(t *testing.T) {
	events := ParseEvents([]byte(`
{"Action":"run","Package":"p","Test":"TestSum"}
{"Action":"output","Package":"p","Test":"TestSum","Output":"panic: test timed out after 3s\n"}
{"Action":"fail","Package":"p","Elapsed":3.17}
`))
	got := Classify(events, errors.New("exit status 1"))
	if got.Outcome != TimedOut {
		t.Errorf("outcome = %s, want %s", got.Outcome, TimedOut)
	}
}

func TestATestThatStartsAndNeverFinishesIsATimeout(t *testing.T) {
	// The binary was killed mid-run, so no panic line was ever written.
	events := ParseEvents([]byte(`
{"Action":"run","Package":"p","Test":"TestLoop"}
{"Action":"fail","Package":"p","Elapsed":10.0}
`))
	if got := Classify(events, errors.New("signal: killed")); got.Outcome != TimedOut {
		t.Errorf("outcome = %s, want %s", got.Outcome, TimedOut)
	}
}

func TestUnexplainedOutputIsNeverAKill(t *testing.T) {
	// The go command writes plain text to stderr for early serious errors.
	// Anything without a terminal event must be an error, not evidence.
	if got := Classify(nil, errors.New("go: cannot find module")); got.Outcome != Errored {
		t.Errorf("outcome = %s, want %s", got.Outcome, Errored)
	}
	if got := Classify(ParseEvents([]byte("go: some plain text\nnot json\n")), nil); got.Outcome != Errored {
		t.Errorf("outcome = %s, want %s", got.Outcome, Errored)
	}
}

func TestSubtestFailurePropagates(t *testing.T) {
	events := ParseEvents([]byte(`
{"Action":"run","Package":"p","Test":"TestTable"}
{"Action":"run","Package":"p","Test":"TestTable/big"}
{"Action":"fail","Package":"p","Test":"TestTable/big","Elapsed":0}
{"Action":"fail","Package":"p","Test":"TestTable","Elapsed":0.01}
{"Action":"fail","Package":"p","Elapsed":0.4}
`))
	got := Classify(events, errors.New("exit status 1"))
	if got.Outcome != Killed {
		t.Fatalf("outcome = %s, want %s", got.Outcome, Killed)
	}
	if len(got.KilledBy) != 2 {
		t.Errorf("killedBy = %v, want both the subtest and its parent", got.KilledBy)
	}
}
