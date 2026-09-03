package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Mutant is one deliberate defect: a byte range in a file and what to put
// there instead.
//
// Every operator is a byte splice rather than an AST re-print, and the
// replacement always carries the same number of newlines as the text it
// replaces. Keeping the file line-for-line identical is what lets coverage
// blocks still map, compiler positions still mean something, and the report
// show an exact before/after pair.
type Mutant struct {
	ID          string   `json:"id"`
	Operator    string   `json:"operator"`
	File        string   `json:"file"` // repo-relative
	Package     string   `json:"package"`
	Function    string   `json:"function"`
	Line        int      `json:"line"`
	Col         int      `json:"col"`
	Start       int      `json:"-"` // byte offsets into the original source
	End         int      `json:"-"`
	Replacement string   `json:"-"`
	Before      string   `json:"before"` // the original line
	After       string   `json:"after"`  // the mutated line
	Outcome     Outcome  `json:"outcome"`
	KilledBy    []string `json:"killedBy,omitempty"`
	Detail      string   `json:"detail,omitempty"`
	DurationMS  int64    `json:"durationMs"`
}

// computeID derives a content hash so a mutant keeps its identity across runs,
// across budget truncation, and across resumes. Sequence numbers would not.
func computeID(file string, line, col int, op, old, new string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s|%s|%s", file, line, col, op, old, new)))
	return hex.EncodeToString(h[:])[:12]
}

// Apply returns the mutated file contents.
func (m Mutant) Apply(src []byte) []byte {
	out := make([]byte, 0, len(src))
	out = append(out, src[:m.Start]...)
	out = append(out, m.Replacement...)
	out = append(out, src[m.End:]...)
	return out
}

// blank replaces text with spaces, keeping newlines so line numbers hold.
func blank(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// lineAt extracts the single source line containing a byte offset.
func lineAt(src []byte, off int) string {
	if off < 0 || off > len(src) {
		return ""
	}
	start := strings.LastIndexByte(string(src[:off]), '\n') + 1
	end := strings.IndexByte(string(src[off:]), '\n')
	if end < 0 {
		end = len(src)
	} else {
		end += off
	}
	return strings.TrimSpace(string(src[start:end]))
}
