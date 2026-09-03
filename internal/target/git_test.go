package target

import "testing"

func TestParseUnifiedDiffTakesTheNewSideOnly(t *testing.T) {
	// Every axis measures the code as it now is, so only the new-side spans
	// matter. A hunk that only deletes leaves nothing to measure.
	diff := `diff --git a/pkg/calc.go b/pkg/calc.go
index 111..222 100644
--- a/pkg/calc.go
+++ b/pkg/calc.go
@@ -10,3 +10,5 @@ func Sum(xs []int) int {
 	for _, x := range xs {
+		if x < 0 {
+			continue
+		}
 		t += x
@@ -40,4 +42,0 @@ func Old() {
-	gone()
`
	files, err := parseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.Path != "pkg/calc.go" {
		t.Errorf("path = %q", f.Path)
	}
	if len(f.Ranges) != 1 {
		t.Fatalf("got %d ranges, want 1 — the delete-only hunk adds nothing", len(f.Ranges))
	}
	if f.Ranges[0] != (LineRange{Start: 10, End: 14}) {
		t.Errorf("range = %+v, want {10 14}", f.Ranges[0])
	}
}

func TestParseUnifiedDiffStatuses(t *testing.T) {
	diff := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package p
+
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package p
diff --git a/old.go b/renamed.go
similarity index 90%
rename from old.go
rename to renamed.go
--- a/old.go
+++ b/renamed.go
@@ -3 +3 @@ package p
+var X = 1
`
	files, err := parseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3", len(files))
	}
	want := []struct {
		path   string
		status Status
	}{
		{"new.go", Added},
		{"gone.go", Deleted},
		{"renamed.go", Renamed},
	}
	for i, w := range want {
		if files[i].Path != w.path {
			t.Errorf("file %d: path = %q, want %q", i, files[i].Path, w.path)
		}
		if files[i].Status != w.status {
			t.Errorf("file %d (%s): status = %c, want %c", i, w.path, files[i].Status, w.status)
		}
	}
	if files[2].OldPath != "old.go" {
		t.Errorf("rename should remember where it came from, got %q", files[2].OldPath)
	}
}

func TestParseHunkNewSideHandlesTheSingleLineForm(t *testing.T) {
	// "@@ -3 +3 @@" means one line, per unified-diff convention. Reading the
	// count as zero would silently drop single-line changes.
	tests := []struct {
		hdr          string
		start, count int
	}{
		{"@@ -1,3 +10,5 @@", 10, 5},
		{"@@ -3 +7 @@", 7, 1},
		{"@@ -40,4 +42,0 @@", 42, 0},
		{"@@ -0,0 +1,2 @@ func f()", 1, 2},
	}
	for _, tc := range tests {
		start, count, ok := parseHunkNewSide(tc.hdr)
		if !ok {
			t.Errorf("%q: not parsed", tc.hdr)
			continue
		}
		if start != tc.start || count != tc.count {
			t.Errorf("%q: got (%d,%d), want (%d,%d)", tc.hdr, start, count, tc.start, tc.count)
		}
	}
}

func TestGoFilesExcludesDeletedAndNonGo(t *testing.T) {
	tr := &Target{Files: []ChangedFile{
		{Path: "b.go", Status: Modified},
		{Path: "gone.go", Status: Deleted},
		{Path: "README.md", Status: Modified},
		{Path: "a.go", Status: Added},
	}}
	got := tr.GoFiles()
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}
	if got[0].Path != "a.go" || got[1].Path != "b.go" {
		t.Errorf("not sorted: %q, %q", got[0].Path, got[1].Path)
	}
}
