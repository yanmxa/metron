package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNoConfigIsAValidConfig(t *testing.T) {
	f, path, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if path != "" || f == nil {
		t.Errorf("path = %q, f = %v", path, f)
	}
	if f.Complexity != nil || f.Mutation != nil {
		t.Error("nothing should be set")
	}
}

func TestOnlyStatedFieldsAreSet(t *testing.T) {
	// A config says what differs; everything else keeps the built-in default,
	// so upgrading metron does not silently freeze old defaults in place.
	f, path, err := Load(write(t, `{"complexity": {"maxCognitive": 25}}`))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("the path should be reported so the panel can say where a setting came from")
	}
	if f.Complexity == nil || f.Complexity.MaxCognitive == nil || *f.Complexity.MaxCognitive != 25 {
		t.Fatalf("maxCognitive not read: %+v", f.Complexity)
	}
	if f.Complexity.MaxDelta != nil {
		t.Error("maxDelta was not stated and must stay unset")
	}
	if f.Mutation != nil {
		t.Error("an absent section must stay absent")
	}
}

func TestCommentsAreAllowed(t *testing.T) {
	// A threshold with no recorded reason gets raised by the next person who
	// hits it.
	f, _, err := Load(write(t, `{
  // Raised to today's worst function; ratcheting down as we split them up.
  "complexity": {"maxCognitive": 34, "maxDelta": 0}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Complexity == nil || *f.Complexity.MaxCognitive != 34 {
		t.Fatalf("comment handling broke parsing: %+v", f.Complexity)
	}
}

func TestASlashInsideAStringSurvives(t *testing.T) {
	f, _, err := Load(write(t, `{"mutation": {"budget": "10m"}, "graph": {"minSiblings": 5}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.MutationBudget(time.Minute); got != 10*time.Minute {
		t.Errorf("budget = %v, want 10m", got)
	}
}

func TestATypoIsAnErrorNotASilentNoOp(t *testing.T) {
	// The worst outcome is a setting that looks applied and is not.
	_, _, err := Load(write(t, `{"complexity": {"maxCognitiv": 25}}`))
	if err == nil {
		t.Fatal("a misspelled key must be reported")
	}
}

func TestNonsenseValuesAreRejectedAtLoad(t *testing.T) {
	tests := []struct{ name, body string }{
		{"score above one", `{"mutation": {"minScore": 70}}`},
		{"negative score", `{"mutation": {"minScore": -0.1}}`},
		{"unparseable budget", `{"mutation": {"budget": "ten minutes"}}`},
		{"zero workers", `{"mutation": {"workers": 0}}`},
		{"zero complexity limit", `{"complexity": {"maxCognitive": 0}}`},
		{"negative delta", `{"complexity": {"maxDelta": -1}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Load(write(t, tc.body)); err == nil {
				t.Errorf("%s was accepted", tc.body)
			}
		})
	}
}

func TestBudgetFallsBackWhenUnset(t *testing.T) {
	f, _, _ := Load(t.TempDir())
	if got := f.MutationBudget(3 * time.Minute); got != 3*time.Minute {
		t.Errorf("budget = %v, want the fallback", got)
	}
}
