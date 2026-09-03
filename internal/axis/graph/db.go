// Package graph measures redundancy and global consistency by reading the
// CodeGraph index — the symbols in the repository and the edges between them.
//
// The graph answers questions no single file can: does this new function
// duplicate one that already exists, does anything call it, and does it reach
// its dependencies the way the rest of the repository does.
package graph

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps metron a single static binary
)

const indexDir = ".codegraph"

// IndexPath returns the location of a repository's CodeGraph database.
func IndexPath(root string) string { return filepath.Join(root, indexDir, "codegraph.db") }

// HasIndex reports whether the repository has been indexed.
func HasIndex(root string) bool {
	st, err := os.Stat(IndexPath(root))
	return err == nil && !st.IsDir()
}

// Sync brings the index up to date with the working tree. Measuring a change
// against a stale index would report the previous revision's structure.
//
// A sync failure is not fatal: a slightly stale index still answers most
// questions, and the caller surfaces the staleness as a note.
func Sync(ctx context.Context, root string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codegraph", "sync", root)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codegraph sync: %w", err)
	}
	return nil
}

// Open opens the index read-only.
//
// The CodeGraph daemon may hold the database in WAL mode while we read. When
// the read-only attach fails for that reason we fall back to reading a
// snapshot copy: a measurement must not depend on whether a background daemon
// happens to be running.
func Open(root string) (*sql.DB, func(), error) {
	path := IndexPath(root)
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err == nil {
		if err = db.Ping(); err == nil {
			if _, err = db.Exec("SELECT 1 FROM nodes LIMIT 1"); err == nil {
				return db, func() { _ = db.Close() }, nil
			}
		}
		_ = db.Close()
	}

	tmp, cerr := snapshot(path)
	if cerr != nil {
		return nil, nil, fmt.Errorf("open codegraph index: %w", err)
	}
	db, err = sql.Open("sqlite", "file:"+tmp+"?mode=ro")
	if err != nil {
		_ = os.Remove(tmp)
		return nil, nil, fmt.Errorf("open codegraph snapshot: %w", err)
	}
	return db, func() { _ = db.Close(); _ = os.Remove(tmp) }, nil
}

func snapshot(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()

	dst, err := os.CreateTemp("", "metron-codegraph-*.db")
	if err != nil {
		return "", err
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
