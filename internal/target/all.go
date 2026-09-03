package target

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveAll builds a Target covering every Go file in the repository rather
// than the files one change touched.
//
// This is a different question from the diff-scoped one and it answers fewer
// things honestly. There is no base revision, so nothing can say whether a
// function got worse or whether an edge is new — those readings report as
// unmeasured rather than guessing. What survives is everything about the code
// as it stands now: how hard it is to read, what nothing reaches, and what the
// tests fail to pin.
func ResolveAll(ctx context.Context, dir string) (*Target, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		root = dir // usable outside a repository; only Δ needs git
	}
	root = strings.TrimSpace(root)

	var files []ChangedFile
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		n, cerr := countLines(path)
		if cerr != nil || n == 0 {
			return nil
		}
		files = append(files, ChangedFile{
			Path:   filepath.ToSlash(rel),
			Status: Modified,
			Ranges: []LineRange{{Start: 1, End: n}},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files under %s", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	return &Target{
		Root: root, BaseRef: "", BaseSHA: "",
		HeadDesc: "whole repository", Files: files, WholeRepo: true,
	}, nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		n++
	}
	return n, sc.Err()
}

func skipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "testdata", ".codegraph", ".metron":
		return true
	}
	return strings.HasPrefix(name, ".")
}
