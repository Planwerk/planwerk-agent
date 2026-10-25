package report

import "testing"

// TestExcludedFileLabel pins the reason text the prompt and the Markdown note
// print after an excluded path, including the file count of a vendored
// directory in the singular and the plural.
func TestExcludedFileLabel(t *testing.T) {
	for _, tc := range []struct {
		entry ExcludedFile
		want  string
	}{
		{ExcludedFile{Path: "go.sum", Reason: ExcludedLockfile}, "lock file"},
		{ExcludedFile{Path: "api/zz_generated.deepcopy.go", Reason: ExcludedGenerated}, "generated"},
		{ExcludedFile{Path: "vendor/", Reason: ExcludedVendored, Files: 1}, "vendored, 1 file"},
		{ExcludedFile{Path: "web/node_modules/", Reason: ExcludedVendored, Files: 12}, "vendored, 12 files"},
	} {
		if got := tc.entry.Label(); got != tc.want {
			t.Errorf("%+v.Label() = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

// TestExcludedFileCount verifies a vendored directory entry counts the changed
// files under it and every other entry counts once.
func TestExcludedFileCount(t *testing.T) {
	list := []ExcludedFile{
		{Path: "go.sum", Reason: ExcludedLockfile},
		{Path: "vendor/", Reason: ExcludedVendored, Files: 12},
		{Path: "zz_generated.go", Reason: ExcludedGenerated},
	}
	if got := ExcludedFileCount(list); got != 14 {
		t.Errorf("ExcludedFileCount = %d, want 14", got)
	}
	if got := ExcludedFileCount(nil); got != 0 {
		t.Errorf("ExcludedFileCount(nil) = %d, want 0", got)
	}
}
