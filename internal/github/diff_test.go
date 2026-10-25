package github

import "testing"

const singleFileDiff = `diff --git a/main.go b/main.go
index abc1234..def5678 100644
--- a/main.go
+++ b/main.go
@@ -10,6 +10,8 @@ func main() {
 	fmt.Println("hello")
 	fmt.Println("world")
 	// existing code
+	newLine1()
+	newLine2()
 	fmt.Println("end")
 }
`

func TestParseDiff_SingleFile(t *testing.T) {
	dm := ParseDiff(singleFileDiff)

	// Context lines should be present
	if !dm.Contains("main.go", 10) {
		t.Error("context line 10 should be in diff")
	}
	if !dm.Contains("main.go", 12) {
		t.Error("context line 12 should be in diff")
	}

	// Added lines
	if !dm.Contains("main.go", 13) {
		t.Error("added line 13 should be in diff")
	}
	if !dm.Contains("main.go", 14) {
		t.Error("added line 14 should be in diff")
	}

	// Line outside diff
	if dm.Contains("main.go", 1) {
		t.Error("line 1 should NOT be in diff")
	}
	if dm.Contains("main.go", 50) {
		t.Error("line 50 should NOT be in diff")
	}
}

const multipleFilesDiff = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"

 func init() {}
diff --git a/b.go b/b.go
index 3333333..4444444 100644
--- a/b.go
+++ b/b.go
@@ -5,3 +5,4 @@ func handler() {
 	doSomething()
 	doMore()
+	doNew()
 }
`

func TestParseDiff_MultipleFiles(t *testing.T) {
	dm := ParseDiff(multipleFilesDiff)

	if !dm.Contains("a.go", 2) {
		t.Error("a.go line 2 (added import) should be in diff")
	}
	if !dm.Contains("b.go", 7) {
		t.Error("b.go line 7 (added call) should be in diff")
	}
	if dm.Contains("a.go", 10) {
		t.Error("a.go line 10 should NOT be in diff")
	}
	if dm.Contains("c.go", 1) {
		t.Error("non-existent file c.go should not be in diff")
	}
}

const multipleHunksDiff = `diff --git a/handler.go b/handler.go
index 5555555..6666666 100644
--- a/handler.go
+++ b/handler.go
@@ -3,4 +3,5 @@ func first() {
 	a := 1
 	b := 2
+	c := 3
 }
@@ -20,4 +21,5 @@ func second() {
 	x := 10
 	y := 20
+	z := 30
 }
`

func TestParseDiff_MultipleHunks(t *testing.T) {
	dm := ParseDiff(multipleHunksDiff)

	// First hunk
	if !dm.Contains("handler.go", 5) {
		t.Error("handler.go line 5 (added in first hunk) should be in diff")
	}

	// Second hunk
	if !dm.Contains("handler.go", 23) {
		t.Error("handler.go line 23 (added in second hunk) should be in diff")
	}

	// Between hunks
	if dm.Contains("handler.go", 15) {
		t.Error("handler.go line 15 (between hunks) should NOT be in diff")
	}
}

const renamedFileDiff = `diff --git a/old.go b/new.go
similarity index 90%
rename from old.go
rename to new.go
index 7777777..8888888 100644
--- a/old.go
+++ b/new.go
@@ -1,3 +1,4 @@
 package main
+// renamed file

 func renamed() {}
`

func TestParseDiff_RenamedFile(t *testing.T) {
	dm := ParseDiff(renamedFileDiff)

	// Should use the new filename
	if !dm.Contains("new.go", 2) {
		t.Error("new.go line 2 should be in diff")
	}
	// Old name should not exist
	if dm.Contains("old.go", 2) {
		t.Error("old.go should not be in diff (was renamed)")
	}
}

// quotedAndSpacedDiff is a pull request diff in the form GitHub serves it:
// git C-quotes a name holding a byte above 0x7F, and ends a ---/+++ line with
// a TAB when the name holds a space, quoted or not. The "+++ not a header"
// line is an added line whose text starts with "++ ".
const quotedAndSpacedDiff = "diff --git a/docs/my file.md b/docs/my file.md\n" +
	"index a973874..d8d53d0 100644\n" +
	"--- a/docs/my file.md\t\n" +
	"+++ b/docs/my file.md\t\n" +
	"@@ -1 +1,3 @@\n" +
	" # Title\n" +
	"+More.\n" +
	"+++ not a header\n" +
	"diff --git \"a/\\303\\244.go\" \"b/\\303\\244.go\"\n" +
	"index 823aafd..b54dead 100644\n" +
	"--- \"a/\\303\\244.go\"\n" +
	"+++ \"b/\\303\\244.go\"\n" +
	"@@ -1 +1,2 @@\n" +
	" package x\n" +
	"+// added\n" +
	"diff --git \"a/\\303\\244 b.go\" \"b/\\303\\244 b.go\"\n" +
	"new file mode 100644\n" +
	"index 0000000..823aafd\n" +
	"--- /dev/null\n" +
	"+++ \"b/\\303\\244 b.go\"\t\n" +
	"@@ -0,0 +1 @@\n" +
	"+package x\n"

func TestParseDiff_QuotedAndSpacedPaths(t *testing.T) {
	dm := ParseDiff(quotedAndSpacedDiff)

	const nonASCII = "\u00e4.go"
	if !dm.Contains(nonASCII, 2) {
		t.Errorf("%q line 2 should be in diff (quoted +++ name)", nonASCII)
	}
	if !dm.Contains("docs/my file.md", 2) {
		t.Error("docs/my file.md line 2 should be in diff (TAB after a spaced name)")
	}
	if dm.Contains("docs/my file.md\t", 2) {
		t.Error("the trailing TAB should not be part of the file name")
	}
	if !dm.Contains("docs/my file.md", 3) {
		t.Error(`an added line starting with "++ " should stay a hunk line`)
	}
	const quotedSpaced = "\u00e4 b.go"
	if !dm.Contains(quotedSpaced, 1) {
		t.Errorf("%q line 1 should be in diff (TAB stripped before unquoting)", quotedSpaced)
	}
}

func TestDiffHeaderPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "b/main.go", "main.go"},
		{"spaced", "b/docs/my file.md\t", "docs/my file.md"},
		{"quoted non-ASCII", "\"b/\\303\\244.go\"", "\u00e4.go"},
		{"quoted and spaced", "\"b/\\303\\244 b.go\"\t", "\u00e4 b.go"},
		{"quoted escapes", "\"b/tab\\ttab and \\\"quote\\\".go\"", "tab\ttab and \"quote\".go"},
		{"unterminated quote", "\"b/broken.go", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diffHeaderPath(tt.in); got != tt.want {
				t.Errorf("diffHeaderPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDiffMap_Contains_NilSafety(t *testing.T) {
	var dm *DiffMap
	if dm.Contains("any.go", 1) {
		t.Error("nil DiffMap should return false")
	}

	dm = &DiffMap{}
	if dm.Contains("any.go", 1) {
		t.Error("empty DiffMap should return false")
	}
}

func TestParseHunkNewStart(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"@@ -10,6 +10,8 @@ func main() {", 10},
		{"@@ -1,3 +1,4 @@", 1},
		{"@@ -20,4 +21,5 @@ func second() {", 21},
		{"@@ -0,0 +1,5 @@", 1},
		{"@@ -5 +5,2 @@", 5},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseHunkNewStart(tt.input)
			if got != tt.want {
				t.Errorf("parseHunkNewStart(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
