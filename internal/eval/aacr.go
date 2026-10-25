package eval

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// The two sides an AACR-Bench comment can sit on, as AACRRow.Side spells them
// once the loader has lowercased it.
const (
	aacrRight = "right"
	aacrLeft  = "left"
)

// AACRRow is one row of an AACR-Bench export: one review comment on one pull
// request, with the JSON keys the export uses. The export names the commits
// the reverse of GitHub's wording, so SourceCommit is the base and
// TargetCommit the head.
type AACRRow struct {
	Language string `json:"project_main_language"`
	PRURL    string `json:"pr_url"`
	// SourceCommit is the BASE: the target-branch commit the pull request was
	// opened against.
	SourceCommit string `json:"pr_source_commit"`
	// TargetCommit is the HEAD: the pull request revision under review, whose
	// lines the right-side comments cite.
	TargetCommit string `json:"pr_target_commit"`
	ChangeLines  int    `json:"pr_change_line_count"`
	PRCategory   string `json:"pr_category"`
	IsAIComment  bool   `json:"is_ai_comment"`
	// Note is the comment's text.
	Note string `json:"note"`
	Path string `json:"path"`
	// Side is "right" for a comment on the revision's lines and "left" for one
	// on lines the pull request deletes.
	Side        string `json:"side"`
	SourceModel string `json:"source_model"`
	FromLine    int    `json:"from_line"`
	ToLine      int    `json:"to_line"`
	Category    string `json:"category"`
	// Context is "Diff Level", "File Level" or "Repo Level": how far beyond
	// the diff the comment reaches.
	Context string `json:"context"`
	// Label is 1 when an expert verified the comment and 0 when it is wrong.
	Label int `json:"label"`
}

// AACRSource is the pull request an AACR-Bench case reviews, as the loader
// builds it from the export's rows. PrepareAACR sets Store and MergeBase;
// setupAACRRepo needs both.
type AACRSource struct {
	URL    string
	Owner  string
	Repo   string
	Number int
	// CloneURL is https://github.com/<owner>/<repo>.git; tests point it at a
	// file:// repository.
	CloneURL string
	// BaseSHA is the export's pr_source_commit, HeadSHA its pr_target_commit.
	BaseSHA string
	HeadSHA string
	// Language and Context are the sampling stratum (SampleAACR): the
	// repository's main language, and the most frequent context of the scored
	// comments, a tie going to the context that sorts first.
	Language string
	Context  string
	// LeftExcluded counts the verified comments on the left side, which the
	// review cannot cite and the score leaves out.
	LeftExcluded int
	// Rows holds every row of the pull request, label 0 included.
	Rows []AACRRow
	// Store is the bare cache repository and MergeBase the merge base of
	// BaseSHA and HeadSHA in it.
	Store     string
	MergeBase string
}

// AACRExport is a loaded AACR-Bench export: one case per pull request with at
// least one verified right-side comment, sorted by name; every validated row
// in export order, those of the pull requests that are no case included; and
// the SHA-256 of the file, which a report records so two reports show whether
// they scored the same export.
type AACRExport struct {
	Cases  []Case
	Rows   []AACRRow
	SHA256 string
}

// commitRe matches a full commit SHA, the only form the export carries.
var commitRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// LoadAACR reads and validates the AACR-Bench export at path, a JSON array
// (the Hugging Face dataset.json) or JSON Lines, and builds its cases. Each
// case expects the verified (label 1) right-side comments of one pull request
// as findings over [from_line, to_line], with the comment in Comment and no
// severity or keywords: scoring matches on lines alone (ScoreRun). Rows
// normalize side to lower case and a from_line after its to_line to the
// ordered range; any other malformed row, a pull request whose rows disagree
// on a commit or the language, and an export with no case are errors. The
// file is never vendored: the caller passes a downloaded copy.
func LoadAACR(path string) (AACRExport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AACRExport{}, fmt.Errorf("reading AACR-Bench export %s: %w", path, err)
	}
	rows, err := decodeAACRRows(data)
	if err != nil {
		return AACRExport{}, fmt.Errorf("parsing AACR-Bench export %s: %w", path, err)
	}
	if len(rows) == 0 {
		return AACRExport{}, fmt.Errorf("AACR-Bench export %s holds no rows", path)
	}
	sources, err := groupAACRRows(rows)
	if err != nil {
		return AACRExport{}, fmt.Errorf("AACR-Bench export %s: %w", path, err)
	}
	cases, err := aacrCases(sources)
	if err != nil {
		return AACRExport{}, fmt.Errorf("AACR-Bench export %s: %w", path, err)
	}
	if len(cases) == 0 {
		return AACRExport{}, fmt.Errorf("AACR-Bench export %s holds no pull request with a verified right-side comment", path)
	}
	sum := sha256.Sum256(data)
	return AACRExport{Cases: cases, Rows: rows, SHA256: hex.EncodeToString(sum[:])}, nil
}

// decodeAACRRows decodes data as one JSON array of rows or, when its first
// non-space byte opens no array, as JSON Lines. Either way it decodes one row
// at a time, so an error names the index of the row it stopped at.
func decodeAACRRows(data []byte) ([]AACRRow, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var rows []AACRRow
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		for dec.More() {
			var row AACRRow
			if err := dec.Decode(&row); err != nil {
				return nil, fmt.Errorf("row %d: %w", len(rows), err)
			}
			rows = append(rows, row)
		}
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("closing the array: %w", err)
		}
		if dec.More() {
			return nil, errors.New("data follows the array")
		}
		return rows, nil
	}
	for {
		var row AACRRow
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", len(rows), err)
		}
		rows = append(rows, row)
	}
}

// groupAACRRows validates every row in place and groups the rows by pull
// request, in order of first appearance. Row errors name the row's index and
// its pr_url.
func groupAACRRows(rows []AACRRow) ([]*AACRSource, error) {
	var sources []*AACRSource
	byURL := make(map[string]*AACRSource)
	firstRow := make(map[*AACRSource]int)
	for i := range rows {
		r := &rows[i]
		owner, repo, number, err := parseAACRURL(r.PRURL)
		if err != nil {
			return nil, fmt.Errorf("row %d (%s): %w", i, r.PRURL, err)
		}
		if err := normalizeAACRRow(r); err != nil {
			return nil, fmt.Errorf("row %d (%s): %w", i, r.PRURL, err)
		}
		url := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
		src := byURL[url]
		if src == nil {
			src = &AACRSource{
				URL: url, Owner: owner, Repo: repo, Number: number,
				CloneURL: aacrCloneURL(owner, repo),
				BaseSHA:  r.SourceCommit, HeadSHA: r.TargetCommit, Language: r.Language,
			}
			byURL[url] = src
			firstRow[src] = i
			sources = append(sources, src)
		}
		for _, f := range []struct{ field, want, got string }{
			{"pr_source_commit", src.BaseSHA, r.SourceCommit},
			{"pr_target_commit", src.HeadSHA, r.TargetCommit},
			{"project_main_language", src.Language, r.Language},
		} {
			if f.got != f.want {
				return nil, fmt.Errorf("pull request %s: row %d gives %s %q, row %d gives %q", url, firstRow[src], f.field, f.want, i, f.got)
			}
		}
		src.Rows = append(src.Rows, *r)
	}
	return sources, nil
}

// parseAACRURL parses a pr_url, which must be a GitHub pull request URL
// (github.ParseRef's URL form) whose owner and repository can name a
// directory of the cache.
func parseAACRURL(u string) (owner, repo string, number int, err error) {
	if !strings.HasPrefix(u, "https://github.com/") {
		return "", "", 0, errors.New("pr_url is not a GitHub pull request URL")
	}
	owner, repo, number, err = github.ParseRef(u)
	if err != nil {
		return "", "", 0, fmt.Errorf("pr_url is not a GitHub pull request URL: %w", err)
	}
	for _, name := range []string{owner, repo} {
		if name == "." || name == ".." {
			return "", "", 0, fmt.Errorf("pr_url names %q, which is no GitHub owner or repository", name)
		}
	}
	return owner, repo, number, nil
}

// aacrCloneURL returns the URL a benchmark repository is fetched from.
func aacrCloneURL(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}

// normalizeAACRRow checks one row's fields and normalizes it: both commits
// lower-case full SHAs, side lower case, and [from_line, to_line] ordered.
func normalizeAACRRow(r *AACRRow) error {
	for _, c := range []struct{ field, sha string }{
		{"pr_source_commit", r.SourceCommit},
		{"pr_target_commit", r.TargetCommit},
	} {
		if !commitRe.MatchString(c.sha) {
			return fmt.Errorf("%s %q is not a full commit SHA", c.field, c.sha)
		}
	}
	r.SourceCommit, r.TargetCommit = strings.ToLower(r.SourceCommit), strings.ToLower(r.TargetCommit)
	if r.Path == "" {
		return errors.New("path is empty")
	}
	r.Side = strings.ToLower(r.Side)
	if r.Side != aacrRight && r.Side != aacrLeft {
		return fmt.Errorf("side %q is neither right nor left", r.Side)
	}
	if r.Label != 0 && r.Label != 1 {
		return fmt.Errorf("label %d is neither 0 nor 1", r.Label)
	}
	if r.FromLine < 1 || r.ToLine < 1 {
		return fmt.Errorf("lines %d-%d do not start at 1 or later", r.FromLine, r.ToLine)
	}
	r.FromLine, r.ToLine = min(r.FromLine, r.ToLine), max(r.FromLine, r.ToLine)
	return nil
}

// aacrCases builds one case per source with at least one verified right-side
// row, named <repo>-<number>, or <owner>-<repo>-<number> when the repository
// name occurs under two owners among those cases, and sorted by name. A pull
// request that is no case never renames one that is.
func aacrCases(sources []*AACRSource) ([]Case, error) {
	var cases []Case
	owners := make(map[string]map[string]bool)
	for _, src := range sources {
		var findings []ExpectedFinding
		contexts := make(map[string]int)
		for _, r := range src.Rows {
			if r.Label != 1 {
				continue
			}
			if r.Side == aacrLeft {
				src.LeftExcluded++
				continue
			}
			findings = append(findings, ExpectedFinding{File: r.Path, Line: r.FromLine, LineEnd: r.ToLine, Comment: r.Note})
			contexts[r.Context]++
		}
		if len(findings) == 0 {
			continue
		}
		src.Context = dominantContext(contexts)
		cases = append(cases, Case{Expected: Expected{Description: src.URL, Findings: findings}, AACR: src})

		repo := strings.ToLower(src.Repo)
		if owners[repo] == nil {
			owners[repo] = make(map[string]bool)
		}
		owners[repo][strings.ToLower(src.Owner)] = true
	}

	named := make(map[string]string)
	for i := range cases {
		src := cases[i].AACR
		name := src.Repo + "-" + strconv.Itoa(src.Number)
		if len(owners[strings.ToLower(src.Repo)]) > 1 {
			name = src.Owner + "-" + name
		}
		if other, ok := named[name]; ok {
			return nil, fmt.Errorf("pull requests %s and %s both map to case name %s", other, src.URL, name)
		}
		named[name] = src.URL
		cases[i].Name = name
	}
	slices.SortFunc(cases, func(a, b Case) int { return strings.Compare(a.Name, b.Name) })
	return cases, nil
}

// dominantContext returns the context counts holds most often, a tie going to
// the one that sorts first.
func dominantContext(counts map[string]int) string {
	var best string
	for ctx, n := range counts {
		if n > counts[best] || n == counts[best] && ctx < best {
			best = ctx
		}
	}
	return best
}

// SampleAACR draws n of the AACR-Bench cases, deterministically and whatever
// order they come in. It rotates over the languages, sorted, and over the
// contexts, sorted: in round r, language i takes the next case of the context
// at position (i+r) modulo the number of contexts, or, when its language has
// none left there, of the next context round the cycle that has one. A
// language with no case left is skipped. Within one language and context the
// cases are ordered by the SHA-256 of their pull request URL. The sample is
// sorted by name; n at or above len(cases) returns every case.
func SampleAACR(cases []Case, n int) []Case {
	type cell struct{ language, context string }
	queues := make(map[cell][]Case)
	var languages, contexts []string
	for _, c := range cases {
		if c.AACR == nil {
			continue
		}
		k := cell{c.AACR.Language, c.AACR.Context}
		queues[k] = append(queues[k], c)
		languages = append(languages, k.language)
		contexts = append(contexts, k.context)
	}
	languages = slices.Compact(slices.Sorted(slices.Values(languages)))
	contexts = slices.Compact(slices.Sorted(slices.Values(contexts)))
	for k, q := range queues {
		slices.SortFunc(q, func(a, b Case) int {
			ha, hb := sha256.Sum256([]byte(a.AACR.URL)), sha256.Sum256([]byte(b.AACR.URL))
			return cmp.Or(bytes.Compare(ha[:], hb[:]), strings.Compare(a.Name, b.Name))
		})
		queues[k] = q
	}

	var picks []Case
	for round := 0; len(picks) < n; round++ {
		picked := false
		for i, lang := range languages {
			if len(picks) == n {
				break
			}
			for step := range contexts {
				k := cell{lang, contexts[(i+round+step)%len(contexts)]}
				if q := queues[k]; len(q) > 0 {
					picks = append(picks, q[0])
					queues[k] = q[1:]
					picked = true
					break
				}
			}
		}
		if !picked {
			break
		}
	}
	slices.SortFunc(picks, func(a, b Case) int { return strings.Compare(a.Name, b.Name) })
	return picks
}

// FindAACRCase returns the case named name among every candidate of the
// export, sampled or not, for -case.
func FindAACRCase(cases []Case, name string) (Case, error) {
	for _, c := range cases {
		if c.Name == name {
			return c, nil
		}
	}
	return Case{}, fmt.Errorf("no AACR-Bench case %q among the export's %d candidates; a case is named <repo>-<number>", name, len(cases))
}

// DefaultAACRCacheDir returns the directory PrepareAACR fetches the benchmark
// repositories into when -aacr-cache is not given: planwerk-eval/aacr under
// the user cache directory, or planwerk-eval-aacr under the temp directory
// when there is none, as internal/cache resolves its own.
func DefaultAACRCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "planwerk-eval", "aacr")
	}
	return filepath.Join(os.TempDir(), "planwerk-eval-aacr")
}
