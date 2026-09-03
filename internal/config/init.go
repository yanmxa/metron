package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Starter renders a config that meets a repository where it is.
//
// Handing someone a tool that fails on their first run is how a tool gets
// switched off. An existing codebase has existing complexity, and a limit
// pitched at where it should be rather than where it is produces a red build
// on day one with no change to blame.
//
// So the limit starts at today's worst function and the delta starts at zero:
// what is already here is tolerated, and none of it may grow. The written file
// says that in a comment, because a threshold with no recorded reason gets
// raised by the next person who hits it.
func Starter(worstCognitive int, hasTests bool) string {
	var b strings.Builder

	b.WriteString("{\n")
	b.WriteString("  // Written by `metron init`.\n")
	b.WriteString("  //\n")
	b.WriteString("  // maxCognitive starts at this repository's worst function today, so the\n")
	b.WriteString("  // first run passes and every later one is a real signal. It is a ratchet:\n")
	b.WriteString("  // lower it whenever you split that function up, and never raise it.\n")
	b.WriteString("  //\n")
	b.WriteString("  // maxDelta of 0 is what makes that honest — existing complexity is\n")
	b.WriteString("  // tolerated, new complexity in an existing function is not.\n")
	fmt.Fprintf(&b, "  \"complexity\": {\n    \"maxCognitive\": %d,\n    \"maxDelta\": 0\n  }", Ratchet(worstCognitive))

	if hasTests {
		b.WriteString(",\n\n")
		b.WriteString("  // Raise minScore as the suite improves. 0.70 is a starting point, not a\n")
		b.WriteString("  // standard; run `metron --since main --axes all` to see where you are.\n")
		b.WriteString("  \"mutation\": {\n    \"minScore\": 0.70,\n    \"budget\": \"10m\"\n  }")
	}
	b.WriteString("\n}\n")
	return b.String()
}

// DefaultMaxCognitive is the limit a tidy repository gets.
const DefaultMaxCognitive = 15

// Ratchet returns the limit to start at: today's worst function, or the default
// when the repository is already inside it.
//
// It leaves no headroom above the current worst. Rounding up would license a
// little more complexity than the repository actually has, and the delta
// already covers regression. It also never goes below the default — quietly
// tightening past it is a decision for the maintainer, not for init.
func Ratchet(worst int) int {
	if worst < DefaultMaxCognitive {
		return DefaultMaxCognitive
	}
	return worst
}

// Write creates the config file, refusing to clobber an existing one.
func Write(root, body string) (string, error) {
	path := filepath.Join(root, Name)
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("%s already exists", Name)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return path, err
	}
	return path, nil
}
