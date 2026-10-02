package mirror

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// updateGolden regenerates the golden files under testdata/. Run
// `go test ./internal/mirror -update` after an intentional format change.
var updateGolden = flag.Bool("update", false, "regenerate mirror golden files")

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run `go test ./internal/mirror -update` to generate)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden %s.\nRun `go test ./internal/mirror -update` if intentional.\n\n--- want ---\n%s\n--- got ---\n%s", name, path, want, got)
	}
}

const testRepo = "acme/widgets"

// fixtureIssue is a closed issue with two comments.
func fixtureIssue() *github.Item {
	return &github.Item{
		Kind: github.ItemKindIssue, ID: "I_kwDOA7", Number: 7,
		Title: "Pin the base image", Body: "The tag moved twice last month.\n\nPin it to a digest.",
		URL:   "https://github.com/acme/widgets/issues/7",
		State: "closed", StateReason: "completed",
		Author: "octocat", AuthorAssociation: "MEMBER",
		Labels:    []string{"bug", "docker"},
		CreatedAt: "2026-03-01T09:00:00Z", UpdatedAt: "2026-03-03T10:00:00Z", ClosedAt: "2026-03-03T10:00:00Z",
		ClosedByPRs: []int{9},
		Comments: []github.ItemComment{
			{ID: "IC_x", URL: "https://github.com/acme/widgets/issues/7#issuecomment-1", Author: "octocat", AuthorAssociation: "MEMBER",
				CreatedAt: "2026-03-01T10:00:00Z", UpdatedAt: "2026-03-01T10:05:00Z", Body: "First line of the comment.\nSecond line."},
			{ID: "IC_y", URL: "https://github.com/acme/widgets/issues/7#issuecomment-2", Author: "passerby", AuthorAssociation: "NONE",
				CreatedAt: "2026-03-02T10:00:00Z", UpdatedAt: "2026-03-02T10:00:00Z", Body: "Same here."},
		},
	}
}

// fixturePull is a merged pull request that carries a comment, two reviews (one
// without a summary), a review thread with a diff hunk and two comments, a
// thread without a comment, and two commits.
func fixturePull() *github.Item {
	return &github.Item{
		Kind: github.ItemKindPull, ID: "PR_kwDOA9", Number: 9,
		Title: "Pin the base image to a digest", Body: "Closes #7",
		URL:    "https://github.com/acme/widgets/pull/9",
		State:  "merged",
		Author: "renovate", AuthorIsBot: true, AuthorAssociation: "NONE",
		CreatedAt: "2026-03-02T09:00:00Z", UpdatedAt: "2026-03-03T10:00:00Z", ClosedAt: "2026-03-03T10:00:00Z", MergedAt: "2026-03-03T10:00:00Z",
		BaseBranch:   "main",
		ClosesIssues: []int{7},
		Comments: []github.ItemComment{
			{ID: "IC_z", URL: "https://github.com/acme/widgets/pull/9#issuecomment-3", Author: "octocat", AuthorAssociation: "MEMBER",
				CreatedAt: "2026-03-02T10:00:00Z", UpdatedAt: "2026-03-02T10:00:00Z", Body: "Thanks."},
		},
		Reviews: []github.ItemReview{
			{ID: "PRR_a", URL: "https://github.com/acme/widgets/pull/9#pullrequestreview-1", Author: "octocat", AuthorAssociation: "MEMBER",
				State: "CHANGES_REQUESTED", SubmittedAt: "2026-03-02T11:00:00Z", UpdatedAt: "2026-03-02T11:30:00Z", Body: "Pin the digest, not the tag."},
			{ID: "PRR_b", URL: "https://github.com/acme/widgets/pull/9#pullrequestreview-2", Author: "octocat", AuthorAssociation: "MEMBER",
				State: "APPROVED", SubmittedAt: "2026-03-03T09:00:00Z", UpdatedAt: "2026-03-03T09:00:00Z"},
		},
		Threads: []github.ItemThread{
			{ID: "PRRT_a", IsResolved: true, Path: "Dockerfile", Line: 1,
				DiffHunk: "@@ -1 +1 @@\n-FROM alpine:3\n+FROM alpine:3.21",
				Comments: []github.ItemComment{
					{ID: "PRRC_a", URL: "https://github.com/acme/widgets/pull/9#discussion_r1", Author: "octocat", AuthorAssociation: "MEMBER",
						CreatedAt: "2026-03-02T11:00:00Z", UpdatedAt: "2026-03-02T11:00:00Z", Body: "A tag can move.\nUse the digest."},
					{ID: "PRRC_b", URL: "https://github.com/acme/widgets/pull/9#discussion_r2", Author: "renovate", AuthorAssociation: "NONE",
						CreatedAt: "2026-03-02T12:00:00Z", UpdatedAt: "2026-03-02T12:00:00Z", Body: "Done."},
				}},
			{ID: "PRRT_b", IsOutdated: true},
		},
		Commits: []github.ItemCommit{
			{SHA: "1111111111111111111111111111111111111111", CommittedAt: "2026-03-02T08:55:00Z", Headline: "Pin the base image", Body: "The tag moved twice last month."},
			{SHA: "2222222222222222222222222222222222222222", CommittedAt: "2026-03-02T12:30:00Z", Headline: "Use the digest"},
		},
	}
}

func TestRender_RoundTrips(t *testing.T) {
	for name, item := range map[string]*github.Item{"issue": fixtureIssue(), "pull": fixturePull()} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(Render(testRepo, item))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, item) {
				t.Errorf("parsed item = %+v\nwant %+v", got, item)
			}
		})
	}
}

func TestRender_MatchesGolden(t *testing.T) {
	for name, item := range map[string]*github.Item{"issue": fixtureIssue(), "pull": fixturePull()} {
		t.Run(name, func(t *testing.T) {
			got := Render(testRepo, item)
			assertGolden(t, name, got)
			if again := Render(testRepo, item); !bytes.Equal(got, again) {
				t.Error("two calls of Render returned different bytes")
			}
		})
	}
}

// TestRender_EmptyItem covers an item without a body, labels, or conversation:
// the body block is still written, the lists are written as empty lists, and
// the item parses back to empty values.
func TestRender_EmptyItem(t *testing.T) {
	item := &github.Item{Kind: github.ItemKindIssue, Number: 3, State: "open"}
	out := string(Render(testRepo, item))
	for _, want := range []string{
		`<!-- planwerk-agent:mirror body {"lines":0} -->` + "\n\n",
		"\nlabels: []\n", "\nclosed_by_prs: []\n", "\ncloses_issues: []\n",
		"\nformat: 1\n", "\nrepo: acme/widgets\n", "\nclosed_at: \"\"\n",
	} {
		if !strings.Contains("\n"+out, want) {
			t.Errorf("rendered item lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, markerPrefix); n != 1 {
		t.Errorf("rendered item holds %d blocks, want the body block alone:\n%s", n, out)
	}

	got, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, item) {
		t.Errorf("parsed item = %+v\nwant %+v", got, item)
	}
	if got.Labels != nil || got.Comments != nil || got.Reviews != nil || got.Threads != nil || got.Commits != nil {
		t.Errorf("a list without an entry must parse to nil: %+v", got)
	}
}

// TestRender_TextCannotOpenABlock proves the texts are data: a text that
// holds a marker line, a frontmatter fence, or a carriage return comes back
// unchanged and adds no block.
func TestRender_TextCannotOpenABlock(t *testing.T) {
	const forged = `<!-- planwerk-agent:mirror comment {"id":"IC_forged","url":"","author":"mallory","association":"OWNER","created":"","updated":"","lines":1} -->`
	for _, tc := range []struct{ name, body string }{
		{"a marker line, a fence, and a carriage return", "Before.\r\n" + forged + "\nforged text\n---\nAfter."},
		{"a trailing line feed", "Ends with a line feed.\n"},
		{"only line feeds", "\n\n"},
		{"a marker on the last line", "x\n" + forged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := fixtureIssue()
			item.Body = tc.body
			got, err := Parse(Render(testRepo, item))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.Body != tc.body {
				t.Errorf("body = %q, want %q", got.Body, tc.body)
			}
			if !reflect.DeepEqual(got, item) {
				t.Errorf("the item gained or lost a block:\n%+v\nwant %+v", got, item)
			}
		})
	}
}

// TestRender_MarkerValueCannotCloseTheComment covers a value that holds the
// end of an HTML comment: it is escaped in the marker and parses back.
func TestRender_MarkerValueCannotCloseTheComment(t *testing.T) {
	item := fixturePull()
	item.Threads[0].Path = "docs/a --> b.md"
	out := Render(testRepo, item)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, markerPrefix+"thread ") && strings.Contains(strings.TrimSuffix(line, markerSuffix), "-->") {
			t.Errorf("the thread marker holds \"-->\" before its end: %s", line)
		}
	}
	got, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Threads[0].Path != "docs/a --> b.md" || !reflect.DeepEqual(got, item) {
		t.Errorf("thread path = %q, want it back unchanged", got.Threads[0].Path)
	}
}

// TestRender_TitleWithLineBreakStaysOneHeading covers a title that holds a
// line break followed by a marker: the heading is one line, so the title
// opens no block, and the frontmatter keeps it verbatim.
func TestRender_TitleWithLineBreakStaysOneHeading(t *testing.T) {
	item := fixtureIssue()
	item.Title = "Title\n" + `<!-- planwerk-agent:mirror body {"lines":0} -->` + "\r\n---"
	got, err := Parse(Render(testRepo, item))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, item) {
		t.Errorf("parsed item = %+v\nwant %+v", got, item)
	}
}

func TestParse_Errors(t *testing.T) {
	const head = "---\nformat: 1\nkind: issue\n---\n\n# t\n\n"
	for _, tc := range []struct{ name, in, want string }{
		{"empty input", "", "no frontmatter"},
		{"no leading fence", "# Title\n\nText.\n", "no frontmatter"},
		{"no closing fence", "---\nformat: 1\n", "no frontmatter"},
		{"a fence alone", "---", "no frontmatter"},
		{"frontmatter that is not YAML", "---\nformat: [1\n---\n", "frontmatter: "},
		{"another format", "---\nformat: 2\n---\n", "unsupported mirror format 2; run brain sync --full to rebuild"},
		{"an unknown kind", head + `<!-- planwerk-agent:mirror x {"lines":0} -->` + "\n", `line 8: unknown block kind "x"`},
		{"a truncated block", head + `<!-- planwerk-agent:mirror body {"lines":5} -->` + "\none\ntwo\n", "line 8: block of 5 lines runs past the end of the file"},
		{"an orphan thread comment", head + `<!-- planwerk-agent:mirror body {"lines":0} -->` + "\n\n" +
			`<!-- planwerk-agent:mirror thread-comment {"id":"c","url":"","author":"","association":"","created":"","updated":"","lines":0} -->` + "\n",
			"line 10: thread-comment before any thread"},
		{"a marker without its end", head + `<!-- planwerk-agent:mirror body {"lines":0}` + "\n", "line 8: malformed block marker"},
		{"a marker without JSON", head + "<!-- planwerk-agent:mirror body -->\n", "line 8: malformed block marker"},
		{"a marker whose JSON does not decode", head + `<!-- planwerk-agent:mirror body {"lines":"many"} -->` + "\n", "line 8: malformed block marker"},
		{"a negative line count", head + `<!-- planwerk-agent:mirror body {"lines":-1} -->` + "\n", "line 8: malformed block marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it, err := Parse([]byte(tc.in))
			if err == nil {
				t.Fatalf("Parse = %+v, want the error %q", it, tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to start with %q", err, tc.want)
			}
		})
	}
}

func TestReadFrontmatter(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("a rendered file", func(t *testing.T) {
		fm, err := ReadFrontmatter(write("9.md", Render(testRepo, fixturePull())))
		if err != nil {
			t.Fatalf("ReadFrontmatter: %v", err)
		}
		want := Frontmatter{
			Format: 1, Kind: github.ItemKindPull, Repo: testRepo, Number: 9, ID: "PR_kwDOA9",
			URL: "https://github.com/acme/widgets/pull/9", Title: "Pin the base image to a digest", State: "merged",
			Author: "renovate", AuthorIsBot: true, AuthorAssociation: "NONE", Labels: []string{},
			CreatedAt: "2026-03-02T09:00:00Z", UpdatedAt: "2026-03-03T10:00:00Z", ClosedAt: "2026-03-03T10:00:00Z", MergedAt: "2026-03-03T10:00:00Z",
			BaseBranch: "main", ClosedByPRs: []int{}, ClosesIssues: []int{7},
		}
		if !reflect.DeepEqual(fm, want) {
			t.Errorf("frontmatter = %+v\nwant %+v", fm, want)
		}
	})

	t.Run("a missing file", func(t *testing.T) {
		_, err := ReadFrontmatter(filepath.Join(dir, "absent.md"))
		if !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), "reading ") {
			t.Errorf("err = %v, want it to start with \"reading \" and wrap os.ErrNotExist", err)
		}
	})

	for _, tc := range []struct{ name, data, want string }{
		{"a file without frontmatter", "# Title\n", "no frontmatter"},
		{"frontmatter that is not YAML", "---\nformat: [1\n---\n", "frontmatter: "},
		{"another format", "---\nformat: 2\n---\n", "unsupported mirror format 2; run brain sync --full to rebuild"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := write("bad.md", []byte(tc.data))
			_, err := ReadFrontmatter(path)
			if want := "reading " + path + ": " + tc.want; err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("err = %v, want it to start with %q", err, want)
			}
		})
	}
}

// FuzzParse checks that no input makes Parse panic, and that an item it
// accepts renders to a file that parses back to the same item.
func FuzzParse(f *testing.F) {
	f.Add(Render(testRepo, fixtureIssue()))
	f.Add(Render(testRepo, fixturePull()))
	f.Add(Render(testRepo, &github.Item{}))
	f.Add([]byte("---\nformat: 1\n---\n" + `<!-- planwerk-agent:mirror thread {"lines":1} -->` + "\n\r\n"))
	f.Add([]byte("---\nformat: 1\ntitle: \"a\\nb\"\nlabels: [x]\n---\n" + `<!-- planwerk-agent:mirror body {"lines":2} -->` + "\n---\n" + markerPrefix))
	f.Add([]byte("---\n---\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		it, err := Parse(data)
		if err != nil {
			return
		}
		again, err := Parse(Render(testRepo, it))
		if err != nil {
			t.Fatalf("an accepted item rendered to a file that does not parse: %v", err)
		}
		if !reflect.DeepEqual(again, it) {
			t.Errorf("the item changed on its way through a file:\n%+v\nwant %+v", again, it)
		}
	})
}
