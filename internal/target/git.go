package target

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Resolve builds a Target for everything that changed on this branch relative
// to baseRef, uncommitted work included.
//
// The base is the merge-base rather than baseRef itself. Diffing straight
// against a moving branch would attribute other people's commits to this
// change the moment main advances.
func Resolve(ctx context.Context, dir, baseRef string) (*Target, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	root = strings.TrimSpace(root)

	base, err := git(ctx, root, "merge-base", baseRef, "HEAD")
	if err != nil {
		// A ref with no shared history (or a bare SHA) still deserves a diff.
		if _, e2 := git(ctx, root, "rev-parse", "--verify", baseRef+"^{commit}"); e2 != nil {
			return nil, fmt.Errorf("cannot resolve base ref %q: %w", baseRef, err)
		}
		base = baseRef
	}
	base = strings.TrimSpace(base)

	// Two-dot against the working tree: committed *and* uncommitted changes.
	// Agent output is often still sitting unstaged when you want to measure it.
	out, err := git(ctx, root, "diff", "--unified=0", "--no-color", "--no-ext-diff",
		"--find-renames", "--diff-filter=ACMRD", base, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	files, err := parseUnifiedDiff(out)
	if err != nil {
		return nil, err
	}
	return &Target{
		Root: root, BaseRef: baseRef, BaseSHA: base,
		HeadDesc: "working tree", Files: files,
	}, nil
}

// parseUnifiedDiff pulls out, per file, the line spans that exist in the NEW
// revision. Hunks with a zero-length new side are pure deletions: there is no
// new code there to mutate or measure, so they contribute no range.
func parseUnifiedDiff(diff string) ([]ChangedFile, error) {
	var files []ChangedFile
	var cur *ChangedFile
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &ChangedFile{Status: Modified}

		case cur == nil:
			// preamble noise

		case strings.HasPrefix(line, "new file mode"):
			cur.Status = Added
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = Deleted
		case strings.HasPrefix(line, "rename from "):
			cur.Status = Renamed
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")

		case strings.HasPrefix(line, "--- "):
			// The old-side path. For a deletion the new side is /dev/null, so
			// this is the only place the name appears in a form that is
			// unambiguous even when the path contains spaces.
			p := strings.TrimPrefix(line, "--- ")
			if p != "/dev/null" && cur.Path == "" {
				cur.Path = strings.TrimPrefix(p, "a/")
			}

		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				cur.Status = Deleted
				continue // keep the name recovered from the old side
			}
			cur.Path = strings.TrimPrefix(p, "b/")

		case strings.HasPrefix(line, "@@"):
			start, count, ok := parseHunkNewSide(line)
			if !ok || count == 0 {
				continue
			}
			cur.Ranges = append(cur.Ranges, LineRange{Start: start, End: start + count - 1})
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading diff: %w", err)
	}
	return files, nil
}

// parseHunkNewSide reads the "+c,d" half of "@@ -a,b +c,d @@".
// A bare "+c" means a single line, per unified-diff convention.
func parseHunkNewSide(hdr string) (start, count int, ok bool) {
	i := strings.Index(hdr, "+")
	if i < 0 {
		return 0, 0, false
	}
	rest := hdr[i+1:]
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	s, c, found := strings.Cut(rest, ",")
	start, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if found {
		if count, err = strconv.Atoi(c); err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// ShowFile returns a file's contents at a revision. Missing paths come back as
// (nil, nil) — a file that did not exist in the base is not an error, it is a
// new file, and the complexity axis needs to tell those apart.
func ShowFile(ctx context.Context, root, rev, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "show", rev+":"+path)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 128 {
			return nil, nil
		}
		return nil, fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return out, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}
