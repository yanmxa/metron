package mutation

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const storeDir = ".metron"

// Manifest describes the run a set of cached results belongs to.
//
// Every field is part of the cache key. A cached verdict is only meaningful if
// nothing that could change it has moved: the source under test, the tests
// that judge it, the operators that produced the mutant, and the toolchain
// that compiled it.
type Manifest struct {
	Version     int    `json:"version"`
	BaseSHA     string `json:"baseSHA"`
	ScopeDigest string `json:"scopeDigest"`
	ConfigHash  string `json:"configHash"`
	Toolchain   string `json:"toolchain"`

	// Baseline lets a resumed run skip re-measuring the suite. It is only read
	// back when the scope digest matches, which means the code and the tests
	// are byte-identical to when it was taken.
	Baseline *BaselineSnapshot `json:"baseline,omitempty"`
}

// BaselineSnapshot is what a resumed run would otherwise have to re-derive:
// the flaky-test quarantine, the reference duration behind the per-mutant
// timeout, and the full test list the allow-pattern is built from.
type BaselineSnapshot struct {
	Quarantine []string `json:"quarantine"`
	DurationMS int64    `json:"durationMs"`
	AllTests   []string `json:"allTests"`
}

const manifestVersion = 1

// Store persists mutant verdicts so an interrupted run resumes instead of
// starting over.
//
// Results are appended as they land rather than written at the end, so a run
// killed halfway still leaves everything it had already established.
type Store struct {
	dir      string
	manifest Manifest
	f        *os.File
	w        *bufio.Writer
}

// OpenStore prepares the run directory and reports the verdicts that can still
// be trusted.
//
// Reuse is all-or-nothing on purpose. A verdict depends on the mutated source
// *and* on every test that could kill it, so tracking which individual results
// survive an edit would mean modelling that dependency precisely. Discarding
// the lot when anything in scope moves is conservative in the only direction
// that matters: it can waste work, never report a stale verdict as current.
func OpenStore(root, baseSHA, scopeDigest, configHash string, fresh bool) (*Store, map[string]Mutant, *BaselineSnapshot, error) {
	dir := filepath.Join(root, storeDir, "mutation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, nil, err
	}

	want := Manifest{
		Version: manifestVersion, BaseSHA: baseSHA, ScopeDigest: scopeDigest,
		ConfigHash: configHash, Toolchain: runtime.Version(),
	}

	cached := map[string]Mutant{}
	var snap *BaselineSnapshot
	if prev, ok := readManifest(filepath.Join(dir, "manifest.json")); ok && !fresh && matches(prev, want) {
		cached = readResults(filepath.Join(dir, "results.jsonl"))
		snap = prev.Baseline
		want.Baseline = prev.Baseline
	} else {
		// A stale checkpoint is worse than none: start the files clean.
		os.Remove(filepath.Join(dir, "results.jsonl"))
		os.Remove(filepath.Join(dir, "cover.out"))
	}

	s := &Store{dir: dir, manifest: want}
	if err := s.writeManifest(); err != nil {
		return nil, nil, nil, err
	}

	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, nil, err
	}
	s.f, s.w = f, bufio.NewWriter(f)
	return s, cached, snap, nil
}

// SaveBaseline records the suite measurements so the next resume can skip them.
func (s *Store) SaveBaseline(b *Baseline) error {
	if s == nil || b == nil {
		return nil
	}
	s.manifest.Baseline = &BaselineSnapshot{
		Quarantine: b.Quarantined(),
		DurationMS: b.Duration.Milliseconds(),
		AllTests:   b.AllTests,
	}
	return s.writeManifest()
}

func (s *Store) writeManifest() error {
	buf, err := json.MarshalIndent(s.manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "manifest.json"), buf, 0o644)
}

// Record appends one verdict. A mutant that was skipped has no verdict to keep.
func (s *Store) Record(m Mutant) error {
	if s == nil || m.Outcome == "" || m.Outcome == Skipped {
		return nil
	}
	buf, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(buf, '\n')); err != nil {
		return err
	}
	// Flushed per record: the whole point is surviving an abrupt exit.
	return s.w.Flush()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.w.Flush()
	return s.f.Close()
}

// CoverPath is where the baseline profile lives between runs.
func (s *Store) CoverPath() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.dir, "cover.out")
}

func readManifest(path string) (Manifest, bool) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, false
	}
	var got Manifest
	if err := json.Unmarshal(buf, &got); err != nil {
		return Manifest{}, false
	}
	return got, true
}

func matches(got, want Manifest) bool {
	return got.Version == want.Version &&
		got.BaseSHA == want.BaseSHA &&
		got.ScopeDigest == want.ScopeDigest &&
		got.ConfigHash == want.ConfigHash &&
		got.Toolchain == want.Toolchain
}

func readResults(path string) map[string]Mutant {
	out := map[string]Mutant{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m Mutant
		if err := json.Unmarshal(line, &m); err != nil {
			continue // a truncated final line from a killed run is expected
		}
		if m.ID != "" {
			out[m.ID] = m
		}
	}
	return out
}

// ScopeDigest fingerprints every Go file whose content could change a verdict:
// the packages being mutated and the packages whose tests judge them.
func ScopeDigest(root string, dirs []string) (string, error) {
	type entry struct{ path, sum string }
	var entries []entry

	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		err := filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			buf, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			sum := sha256.Sum256(buf)
			entries = append(entries, entry{filepath.ToSlash(rel), hex.EncodeToString(sum[:8])})
			return nil
		})
		if err != nil {
			return "", err
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s:%s\n", e.path, e.sum)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Hash fingerprints the settings that affect a verdict. Reference ranges are
// deliberately absent: they change how a reading is judged, not what it is.
func (c Config) Hash() string {
	ops := append([]string{}, c.Operators...)
	sort.Strings(ops)
	h := sha256.New()
	fmt.Fprintf(h, "ops=%s;baseline=%d;factor=%.2f;tmin=%s;tmax=%s;paranoid=%v",
		strings.Join(ops, ","), c.BaselineRounds, c.TimeoutFactor,
		c.TimeoutMin, c.TimeoutMax, c.Paranoid)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
