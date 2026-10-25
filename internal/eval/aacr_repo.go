package eval

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// aacrGitTimeout bounds each git subprocess that fetches a benchmark
// repository or reads its history or tree: a shallow fetch of a large
// repository, or the checkout of its tree, takes minutes, not gitTimeout's
// seconds.
const aacrGitTimeout = 15 * time.Minute

// aacrDepths are the depths PrepareAACR fetches both commits of a pull
// request at, in order, until their merge base appears.
var aacrDepths = []int{64, 512, 4096, 32768}

// PrepareAACR fetches the two commits of every AACR-Bench case among cases
// into a bare repository of its own under root, root/<owner>/<repo>/<number>.git,
// and records it and the commits' merge base in the case's AACRSource. A
// store that already holds both commits and their merge base is reused
// without a fetch. Otherwise both commits are fetched by SHA at absolute
// depths, deeper each time, until the merge base appears. It works through
// every case and returns one error naming each pull request it could not
// prepare, so the eval stops before it spends a token. It logs each case to
// log. Corpus cases are skipped.
func PrepareAACR(root string, cases []Case, log io.Writer) error {
	return prepareAACR(root, cases, aacrDepths, log)
}

// prepareAACR is PrepareAACR with the depths to try, so a test can fetch a
// small repository at small depths.
func prepareAACR(root string, cases []Case, depths []int, log io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("AACR-Bench cache %s: %w", root, err)
	}
	var errs []error
	for _, c := range cases {
		src := c.AACR
		if src == nil {
			continue
		}
		_, _ = fmt.Fprintf(log, "preparing %s (%s/%s#%d) ...\n", c.Name, src.Owner, src.Repo, src.Number)
		store := filepath.Join(abs, src.Owner, src.Repo, strconv.Itoa(src.Number)+".git")
		if err := prepareStore(store, src, depths); err != nil {
			errs = append(errs, fmt.Errorf("case %s (%s): %w", c.Name, src.URL, err))
		}
	}
	return errors.Join(errs...)
}

// prepareStore makes store a bare repository that holds src's two commits and
// their merge base, and sets src.Store and src.MergeBase. Each pull request
// gets a store of its own because a later fetch at a smaller depth would
// shorten the history an earlier pull request of the same repository needs.
// The fetch passes --depth, never --deepen: GitHub serves a force-pushed
// revision by SHA, but --deepen leaves it at depth 1, while --depth fetches
// its ancestry.
func prepareStore(store string, src *AACRSource, depths []int) error {
	if err := os.MkdirAll(filepath.Dir(store), 0o750); err != nil {
		return err
	}
	if _, err := gitRun(filepath.Dir(store), gitTimeout, nil, "init", "--bare", "-q", store); err != nil {
		return err
	}
	mergeBase, err := gitRun(store, aacrGitTimeout, nil, "merge-base", src.BaseSHA, src.HeadSHA)
	for _, depth := range depths {
		if err == nil {
			break
		}
		if _, err := gitRun(store, aacrGitTimeout, []string{"GIT_TERMINAL_PROMPT=0"},
			"fetch", "--no-tags", "--depth="+strconv.Itoa(depth), src.CloneURL,
			"+"+src.BaseSHA+":refs/planwerk-eval/base", "+"+src.HeadSHA+":refs/planwerk-eval/head"); err != nil {
			return err
		}
		mergeBase, err = gitRun(store, aacrGitTimeout, nil, "merge-base", src.BaseSHA, src.HeadSHA)
	}
	if err != nil {
		return fmt.Errorf("no merge base of %s and %s within %d commits", src.BaseSHA, src.HeadSHA, depths[len(depths)-1])
	}
	src.Store, src.MergeBase = store, mergeBase
	return nil
}

// setupAACRRepo builds the repository an AACR-Bench case is reviewed in, in
// dir, from the store PrepareAACR filled, which it reads through
// objects/info/alternates instead of copying it. Like a corpus repo it holds
// two commits: on main and origin/main a base commit carrying the merge base's
// tree, and on eval/head, checked out, a head commit carrying the revision's
// tree. The review's three-dot diff is then the revision's own diff, even when
// the pull request's base has moved on since it branched. The commit SHAs are
// the harness's own, so headSHA is the head commit's, not the export's.
// changed is the set FetchAndCheckout would compute (github.Client.DiffNames).
// The repository gets no go.mod: decision 106 writes one into corpus repos
// only.
func setupAACRRepo(dir string, src *AACRSource) (headSHA string, changed []string, err error) {
	if src.Store == "" || src.MergeBase == "" {
		return "", nil, errors.New("the benchmark repository is not prepared; run PrepareAACR first")
	}
	if err := initRepo(dir); err != nil {
		return "", nil, err
	}
	alternates := filepath.Join(dir, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(filepath.Join(src.Store, "objects")+"\n"), 0o600); err != nil {
		return "", nil, fmt.Errorf("linking the benchmark cache: %w", err)
	}

	base, err := gitRun(dir, gitTimeout, nil, "commit-tree", "--no-gpg-sign", "-m", "base", src.MergeBase+"^{tree}")
	if err != nil {
		return "", nil, err
	}
	head, err := gitRun(dir, gitTimeout, nil, "commit-tree", "--no-gpg-sign", "-p", base, "-m", "head", src.HeadSHA+"^{tree}")
	if err != nil {
		return "", nil, err
	}
	for _, args := range [][]string{
		{"update-ref", "refs/heads/" + evalBaseBranch, base},
		{"update-ref", "refs/remotes/origin/" + evalBaseBranch, base},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/" + evalBaseBranch},
		{"update-ref", "refs/heads/" + evalHeadBranch, head},
		{"symbolic-ref", "HEAD", "refs/heads/" + evalHeadBranch},
	} {
		if err := git(dir, args...); err != nil {
			return "", nil, err
		}
	}
	// A repository that tracks files in Git LFS checks out their pointers:
	// the smudge filter would fetch from a remote the repository does not
	// have.
	if _, err := gitRun(dir, aacrGitTimeout, []string{"GIT_LFS_SKIP_SMUDGE=1"}, "reset", "-q", "--hard"); err != nil {
		return "", nil, err
	}

	if headSHA, err = gitRun(dir, gitTimeout, nil, "rev-parse", "HEAD"); err != nil {
		return "", nil, err
	}
	if changed, err = (github.Client{}).DiffNames(dir, evalBaseBranch); err != nil {
		return "", nil, err
	}
	return headSHA, changed, nil
}
