package mutation

import (
	"os"
	"path/filepath"
	"testing"
)

func openIn(t *testing.T, root, base, digest, cfg string, fresh bool) (*Store, map[string]Mutant, *BaselineSnapshot) {
	t.Helper()
	s, cached, snap, err := OpenStore(root, base, digest, cfg, fresh)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s, cached, snap
}

func TestVerdictsSurviveAcrossRuns(t *testing.T) {
	root := t.TempDir()
	s, cached, _ := openIn(t, root, "abc", "d1", "c1", false)
	if len(cached) != 0 {
		t.Fatalf("first run should start empty, got %d", len(cached))
	}
	if err := s.Record(Mutant{ID: "m1", Outcome: Killed}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(Mutant{ID: "m2", Outcome: Survived}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, cached, _ = openIn(t, root, "abc", "d1", "c1", false)
	if len(cached) != 2 {
		t.Fatalf("got %d cached verdicts, want 2", len(cached))
	}
	if cached["m2"].Outcome != Survived {
		t.Errorf("m2 = %s, want %s", cached["m2"].Outcome, Survived)
	}
}

func TestAnythingThatCouldChangeAVerdictInvalidatesTheCache(t *testing.T) {
	// Reuse is all-or-nothing: a verdict depends on the mutated source and on
	// every test that judges it, so a stale one must never be reported as
	// current.
	tests := []struct {
		name              string
		base, digest, cfg string
	}{
		{"different base commit", "zzz", "d1", "c1"},
		{"source or tests edited", "abc", "d2", "c1"},
		{"operators or timeouts changed", "abc", "d1", "c2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s, _, _ := openIn(t, root, "abc", "d1", "c1", false)
			s.Record(Mutant{ID: "m1", Outcome: Killed})
			s.Close()

			_, cached, _ := openIn(t, root, tc.base, tc.digest, tc.cfg, false)
			if len(cached) != 0 {
				t.Errorf("got %d cached verdicts, want 0", len(cached))
			}
		})
	}
}

func TestFreshDiscardsAValidCheckpoint(t *testing.T) {
	root := t.TempDir()
	s, _, _ := openIn(t, root, "abc", "d1", "c1", false)
	s.Record(Mutant{ID: "m1", Outcome: Killed})
	s.SaveBaseline(&Baseline{Quarantine: map[string]bool{"TestFlaky": true}})
	s.Close()

	_, cached, snap := openIn(t, root, "abc", "d1", "c1", true)
	if len(cached) != 0 || snap != nil {
		t.Errorf("--fresh kept %d verdicts and snapshot=%v", len(cached), snap != nil)
	}
}

func TestBaselineIsReusedOnlyWithTheCheckpoint(t *testing.T) {
	root := t.TempDir()
	s, _, _ := openIn(t, root, "abc", "d1", "c1", false)
	if err := s.SaveBaseline(&Baseline{
		Quarantine: map[string]bool{"TestFlaky": true},
		AllTests:   []string{"TestA", "TestFlaky"},
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, _, snap := openIn(t, root, "abc", "d1", "c1", false)
	if snap == nil {
		t.Fatal("baseline snapshot was not kept")
	}
	if len(snap.Quarantine) != 1 || snap.Quarantine[0] != "TestFlaky" {
		t.Errorf("quarantine = %v", snap.Quarantine)
	}
	if len(snap.AllTests) != 2 {
		t.Errorf("allTests = %v — the quarantine is unenforceable without it", snap.AllTests)
	}
}

func TestUnscoredMutantsAreNotRecorded(t *testing.T) {
	// A mutant the budget cut off has no verdict, and writing one would make
	// the next run trust a result that was never measured.
	root := t.TempDir()
	s, _, _ := openIn(t, root, "abc", "d1", "c1", false)
	s.Record(Mutant{ID: "skipped", Outcome: Skipped})
	s.Record(Mutant{ID: "blank"})
	s.Record(Mutant{ID: "real", Outcome: Survived})
	s.Close()

	_, cached, _ := openIn(t, root, "abc", "d1", "c1", false)
	if len(cached) != 1 || cached["real"].Outcome != Survived {
		t.Errorf("cached = %v, want only the scored one", cached)
	}
}

func TestATruncatedFinalLineIsTolerated(t *testing.T) {
	// A run killed mid-write is the normal case this whole mechanism exists for.
	root := t.TempDir()
	s, _, _ := openIn(t, root, "abc", "d1", "c1", false)
	s.Record(Mutant{ID: "m1", Outcome: Killed})
	s.Close()

	path := filepath.Join(root, storeDir, "mutation", "results.jsonl")
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(buf, []byte(`{"id":"m2","outcome":"SURV`)...), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cached, _ := openIn(t, root, "abc", "d1", "c1", false)
	if len(cached) != 1 || cached["m1"].Outcome != Killed {
		t.Errorf("cached = %v, want the one complete record", cached)
	}
}

func TestScopeDigestTracksContent(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package p\n")
	write("a_test.go", "package p\n")

	first, err := ScopeDigest(dir, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := ScopeDigest(dir, []string{dir})
	if first != again {
		t.Error("digest is not stable across calls")
	}

	// Editing the tests must invalidate too: they decide the verdicts.
	write("a_test.go", "package p\n// edited\n")
	afterTest, _ := ScopeDigest(dir, []string{dir})
	if afterTest == first {
		t.Error("editing a test file left the digest unchanged")
	}

	write("a.go", "package p\n// edited\n")
	afterSrc, _ := ScopeDigest(dir, []string{dir})
	if afterSrc == afterTest {
		t.Error("editing a source file left the digest unchanged")
	}
}

func TestConfigHashIgnoresReferenceRanges(t *testing.T) {
	// Ranges change how a reading is judged, not what it is, so moving one must
	// not throw away measurements that are still valid.
	a := DefaultConfig()
	b := DefaultConfig()
	b.RefScore, b.RefStrength, b.RefReach, b.MaxObs = 0.99, 0.99, 0.99, 1
	if a.Hash() != b.Hash() {
		t.Error("changing a reference range invalidated the checkpoint")
	}

	c := DefaultConfig()
	c.Operators = []string{OpInvertLogical}
	if a.Hash() == c.Hash() {
		t.Error("changing the operator set must invalidate the checkpoint")
	}
}
