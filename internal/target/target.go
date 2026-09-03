// Package target resolves "what changed" — the input every axis measures against.
//
// A Target is always a local working tree plus a base ref. That is the whole
// premise of metron: it runs your tests and reads your index, so it needs the
// code on disk, not a diff fetched over HTTP.
package target

import (
	"fmt"
	"path/filepath"
	"sort"
)

// LineRange is a 1-based inclusive span of lines in the NEW version of a file.
type LineRange struct {
	Start int
	End   int
}

func (r LineRange) Overlaps(start, end int) bool {
	return r.Start <= end && r.End >= start
}

// Status mirrors git's single-letter change status.
type Status byte

const (
	Added    Status = 'A'
	Modified Status = 'M'
	Deleted  Status = 'D'
	Renamed  Status = 'R'
)

// ChangedFile is one file the change touched, with the line spans it added or
// modified. Deleted files carry no ranges — there is nothing left to measure.
type ChangedFile struct {
	Path    string // repo-relative, forward slashes
	OldPath string // set for renames
	Status  Status
	Ranges  []LineRange
}

// Func is a function or method whose body the change touched.
type Func struct {
	Path      string
	Name      string
	Recv      string // receiver type without the pointer star, "" for plain funcs
	StartLine int    // the `func` keyword
	EndLine   int    // the closing brace
	BodyStart int
	BodyEnd   int
	IsNew     bool // the whole file is new, or the func has no counterpart in base
}

// Key identifies a func across two revisions. Receiver is part of the identity
// because a package can hold several methods of the same name.
func (f Func) Key() string {
	if f.Recv != "" {
		return f.Path + ":(" + f.Recv + ")." + f.Name
	}
	return f.Path + ":" + f.Name
}

func (f Func) Label() string {
	if f.Recv != "" {
		return "(" + f.Recv + ")." + f.Name
	}
	return f.Name
}

// Target is one change under measurement.
type Target struct {
	Root     string // absolute path to the repo root
	BaseRef  string // what the user asked to compare against, e.g. "main"
	BaseSHA  string // the resolved merge-base
	HeadDesc string // human description of the head side ("working tree" or a SHA)
	Files    []ChangedFile
}

// GoFiles returns the changed files that are Go source and still exist,
// excluding tests and generated code — the set worth measuring.
func (t *Target) GoFiles() []ChangedFile {
	var out []ChangedFile
	for _, f := range t.Files {
		if f.Status == Deleted || filepath.Ext(f.Path) != ".go" {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (t *Target) Describe() string {
	return fmt.Sprintf("%s...%s · %d files", t.BaseRef, t.HeadDesc, len(t.Files))
}
