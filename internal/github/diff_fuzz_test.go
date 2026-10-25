package github

import "testing"

func FuzzParseDiff(f *testing.F) {
	f.Add(singleFileDiff)
	f.Add(`diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,3 @@
 line one
+new line
 line two
`)
	f.Add(`diff --git a/empty b/empty
+++ /dev/null
`)
	f.Add("")
	f.Add("@@ -1,1 +1,1 @@\n not in a file\n")
	f.Add("@@ + @@\n")
	f.Add("+++ b/file\n@@ -0 +0 @@\n+x\n")
	f.Add(quotedAndSpacedDiff)
	f.Add("+++ \"b/unterminated\n@@ -0 +0 @@\n+x\n")

	f.Fuzz(func(t *testing.T, raw string) {
		dm := ParseDiff(raw)
		if dm == nil {
			t.Fatal("ParseDiff returned nil")
		}
		// Contains must be safe for any file/line combination.
		dm.Contains("missing", 0)
		dm.Contains("missing", -1)
		for file, lines := range dm.files {
			for line := range lines {
				if !dm.Contains(file, line) {
					t.Fatalf("Contains(%q, %d) false but present in map", file, line)
				}
			}
		}
	})
}
