package mutation

import (
	"fmt"
	"path/filepath"
	"sort"

	"golang.org/x/tools/cover"
)

// CoverIndex answers whether a position in a file was executed by the test
// suite. A mutant on a line no test reaches cannot be killed, so it is
// classified without ever being run — measured at 13% of mutants on a real
// file, obtained for free.
type CoverIndex struct {
	blocks map[string][]cover.ProfileBlock // repo-relative file -> blocks, sorted
	funcs  map[string]bool                 // "file:startLine-endLine" -> any block ran
}

// BuildCoverIndex parses a coverprofile.
//
// Two details decide whether this is correct at all. The profile must have
// been produced with -coverpkg covering the whole module: without it, a
// package whose tests live elsewhere reports zero coverage for every line, and
// every mutant in it would be wrongly written off as unreachable. And the
// merged profile legitimately contains the same block twice with different
// counts, because it concatenates one profile per test binary — so it is
// parsed with x/tools/cover, which merges them correctly rather than letting
// map iteration order decide.
//
// Profile file names are import paths, so dirOf maps them back to disk.
func BuildCoverIndex(path string, dirOf func(importPath string) string, root string) (*CoverIndex, error) {
	profiles, err := cover.ParseProfiles(path)
	if err != nil {
		return nil, fmt.Errorf("parse coverprofile: %w", err)
	}

	ci := &CoverIndex{blocks: map[string][]cover.ProfileBlock{}, funcs: map[string]bool{}}
	for _, p := range profiles {
		rel := relFor(p.FileName, dirOf, root)
		if rel == "" {
			continue
		}
		ci.blocks[rel] = append(ci.blocks[rel], p.Blocks...)
	}
	for f := range ci.blocks {
		b := ci.blocks[f]
		sort.Slice(b, func(i, j int) bool {
			if b[i].StartLine != b[j].StartLine {
				return b[i].StartLine < b[j].StartLine
			}
			return b[i].StartCol < b[j].StartCol
		})
		ci.blocks[f] = b
	}
	return ci, nil
}

// relFor turns "example.com/mod/pkg/file.go" into "pkg/file.go".
func relFor(profileName string, dirOf func(string) string, root string) string {
	dir := filepath.Dir(profileName)
	base := filepath.Base(profileName)
	for p := dir; p != "." && p != "/"; p = filepath.Dir(p) {
		if d := dirOf(p); d != "" {
			rel, err := filepath.Rel(root, filepath.Join(d, base))
			if err != nil {
				return ""
			}
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// Covered reports whether the smallest block enclosing a position ran.
//
// ok is false when no block encloses the position at all. That is not rare:
// Go instruments a case body, not the case guard, so a mutation inside
// `case a && b:` maps to nothing. The caller must then fall back to the
// enclosing function — never to the next block, since a guard is evaluated
// even when its branch is not taken.
func (ci *CoverIndex) Covered(file string, line, col int) (covered, ok bool) {
	var best *cover.ProfileBlock
	for i := range ci.blocks[file] {
		b := &ci.blocks[file][i]
		if !contains(b, line, col) {
			continue
		}
		if best == nil || smaller(b, best) {
			best = b
		}
	}
	if best == nil {
		return false, false
	}
	return best.Count > 0, true
}

// FuncCovered reports whether any block inside a function's line span ran. It
// is the conservative fallback: it can only make metron run a mutant it might
// have skipped, never silently drop one.
func (ci *CoverIndex) FuncCovered(file string, startLine, endLine int) bool {
	for _, b := range ci.blocks[file] {
		if b.StartLine >= startLine && b.EndLine <= endLine && b.Count > 0 {
			return true
		}
	}
	return false
}

// Reachable decides whether a mutant is worth executing.
func (ci *CoverIndex) Reachable(m Mutant, fnStart, fnEnd int) bool {
	if ci == nil {
		return true // no profile: run everything rather than invent coverage
	}
	if covered, ok := ci.Covered(m.File, m.Line, m.Col); ok {
		return covered
	}
	return ci.FuncCovered(m.File, fnStart, fnEnd)
}

func contains(b *cover.ProfileBlock, line, col int) bool {
	if line < b.StartLine || line > b.EndLine {
		return false
	}
	if line == b.StartLine && col < b.StartCol {
		return false
	}
	if line == b.EndLine && col > b.EndCol {
		return false
	}
	return true
}

func smaller(a, b *cover.ProfileBlock) bool {
	as := (a.EndLine-a.StartLine)*1000 + (a.EndCol - a.StartCol)
	bs := (b.EndLine-b.StartLine)*1000 + (b.EndCol - b.StartCol)
	return as < bs
}
