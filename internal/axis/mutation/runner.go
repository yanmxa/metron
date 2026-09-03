package mutation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Runner executes `go test` for one mutant.
type Runner struct {
	Root string
	Env  []string
}

// Invocation is one `go test` run.
type Invocation struct {
	Packages []string // import paths
	Overlay  string   // path to the overlay JSON, empty for a baseline run
	Run      []string // top-level test names to run; empty means all
	Timeout  time.Duration
	Parallel int
	Cover    string // coverprofile path; empty to skip coverage
}

// Test runs the invocation and classifies the result.
//
// Two flags are not negotiable.
//
// `-count=1` defeats the test result cache, which would otherwise hand back a
// previous mutant's verdict.
//
// `-vet=off` is a correctness requirement, not a speed-up. `go test` runs vet
// by default, and a vet failure produces the same build-fail shape a compile
// error does. A perfectly legal mutant — `n >= 0 || n < 0` with `<` flipped to
// `>=`, giving `n >= 0 || n >= 0` — trips vet's redundant-or check and would be
// recorded NOT_VIABLE. That removes a genuine survivor from the denominator
// and inflates the score.
func (r *Runner) Test(ctx context.Context, inv Invocation) (Verdict, time.Duration) {
	args := []string{"test", "-count=1", "-vet=off", "-json"}
	if inv.Timeout > 0 {
		args = append(args, "-timeout="+inv.Timeout.String())
	}
	if inv.Parallel > 0 {
		args = append(args, fmt.Sprintf("-p=%d", min(inv.Parallel, 2)),
			fmt.Sprintf("-parallel=%d", inv.Parallel))
	}
	if inv.Overlay != "" {
		args = append(args, "-overlay="+inv.Overlay)
	}
	if inv.Cover != "" {
		// Without -coverpkg over the whole module, a package whose tests live
		// elsewhere reports zero coverage and every mutant in it looks
		// unreachable. Measured overhead: none.
		args = append(args, "-coverpkg=./...", "-coverprofile="+inv.Cover)
	}
	if len(inv.Run) > 0 {
		args = append(args, "-run="+runPattern(inv.Run))
	}
	args = append(args, inv.Packages...)

	// A hard ceiling above the per-test timeout so a wedged toolchain cannot
	// stall the whole run.
	wall := inv.Timeout + 30*time.Second
	if inv.Timeout == 0 {
		wall = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Dir = r.Root
	cmd.Env = r.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	v := Classify(ParseEvents(stdout.Bytes()), err)
	if v.Outcome == Errored && stderr.Len() > 0 {
		v.Detail = strings.TrimSpace(firstLine([]string{stderr.String()}))
	}
	return v, elapsed
}

// env pins everything that could make two runs of the same commit disagree.
func (r *Runner) env() []string {
	env := append([]string{}, os.Environ()...)
	if r.Env != nil {
		env = append(env, r.Env...)
	}
	return append(env, "GOFLAGS=", "TZ=UTC", "LC_ALL=C")
}

// runPattern builds an anchored alternation.
//
// Only top-level test names go in here. `go test -list` cannot enumerate
// subtests — they are discovered at run time — and selecting one still runs
// its parent's body anyway. Top-level names are Go identifiers, so the
// alternation needs no escaping.
func runPattern(names []string) string {
	sorted := append([]string{}, names...)
	sort.Strings(sorted)
	return "^(" + strings.Join(sorted, "|") + ")$"
}

// ListTests enumerates a package's top-level tests.
func (r *Runner) ListTests(ctx context.Context, pkg string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-list", ".*", pkg)
	cmd.Dir = r.Root
	cmd.Env = r.env()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if testNameRE.MatchString(line) {
			names = append(names, line)
		}
	}
	return names, nil
}

var testNameRE = regexp.MustCompile(`^(Test|Example|Benchmark|Fuzz)[A-Za-z0-9_]*$`)

// WriteOverlay materialises a mutant in a temp directory and writes the
// overlay file the go command reads.
//
// The working tree is never touched. `go test -overlay` honours the mapping at
// compile time — the documented limitation about overlays and `go test` is
// about files a test reads at run time, not about compilation.
func WriteOverlay(dir, root string, m Mutant, src []byte) (string, error) {
	mutated := filepath.Join(dir, "mutant.go")
	if err := os.WriteFile(mutated, m.Apply(src), 0o600); err != nil {
		return "", err
	}
	overlay := map[string]map[string]string{
		"Replace": {filepath.Join(root, m.File): mutated},
	}
	buf, err := json.Marshal(overlay)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
