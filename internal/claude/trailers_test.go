package claude

import (
	"strings"
	"testing"
)

// TestCommittingPromptsHonorTheTrailerBan renders every prompt whose session
// creates commits, with and without a repository ban on AI trailers. Under the
// ban no line of the prompt asks for Assisted-by, neither the trailer block nor
// a spelled-out commit command, while Signed-off-by and the Co-authored-by ban
// stay, and the prompt names the file that set the ban (decision 150). An
// attribution footer stays as the place the assistance is disclosed and does
// not call itself a mirror of the trailer. The
// goldens lock a few of these renderings; this table covers all of them, so a
// new commit command that hard-codes the trailer fails here.
func TestCommittingPromptsHonorTheTrailerBan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		render func(forbiddenBy string) string
	}{
		{"implement", func(f string) string {
			ctx := goldenImplementContext()
			ctx.AITrailersForbiddenBy = f
			return BuildImplementPrompt(ctx)
		}},
		{"implement orchestrated", func(f string) string {
			ctx := goldenImplementContext()
			ctx.WorkerModel = testWorkerModel
			ctx.AITrailersForbiddenBy = f
			return BuildImplementPrompt(ctx)
		}},
		{"implementer agent", buildImplementerAgentPrompt},
		{"bare implement", func(f string) string {
			ctx := goldenBareImplementContext()
			ctx.AITrailersForbiddenBy = f
			return BuildBareImplementPrompt(ctx)
		}},
		{"simplify apply", func(f string) string {
			ctx := goldenSimplifyApplyContext()
			ctx.AITrailersForbiddenBy = f
			return BuildSimplifyApplyPrompt(ctx)
		}},
		{"review apply", func(f string) string {
			ctx := goldenReviewApplyContext()
			ctx.AITrailersForbiddenBy = f
			return BuildReviewApplyPrompt(ctx)
		}},
		{"fix", func(f string) string {
			ctx := goldenFixContext()
			ctx.AITrailersForbiddenBy = f
			return BuildFixPrompt(ctx)
		}},
		{"fix no-fixup", func(f string) string {
			ctx := goldenFixContext()
			ctx.Fixup = false
			ctx.AITrailersForbiddenBy = f
			return BuildFixPrompt(ctx)
		}},
		{"bare fix", func(f string) string {
			ctx := goldenBareFixContext()
			ctx.AITrailersForbiddenBy = f
			return BuildBareFixPrompt(ctx)
		}},
		{"bare fix no-fixup", func(f string) string {
			ctx := goldenBareFixContext()
			ctx.Fixup = false
			ctx.AITrailersForbiddenBy = f
			return BuildBareFixPrompt(ctx)
		}},
		{"address", func(f string) string {
			ctx := goldenAddressContext()
			ctx.AITrailersForbiddenBy = f
			return BuildAddressPrompt(ctx)
		}},
		{"address aggregate", func(f string) string {
			ctx := goldenAddressContext()
			ctx.OneCommitPerThread = false
			ctx.AITrailersForbiddenBy = f
			return BuildAddressPrompt(ctx)
		}},
		{"bare address", func(f string) string {
			ctx := goldenBareAddressContext()
			ctx.AITrailersForbiddenBy = f
			return BuildBareAddressPrompt(ctx)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			banned := tc.render(goldenAITrailersForbiddenBy)
			if strings.Contains(banned, "Assisted-by: Claude") {
				t.Error("under the ban the prompt still asks for an Assisted-by trailer")
			}
			for _, want := range []string{
				"`" + goldenAITrailersForbiddenBy + "` on the base branch forbids attributing commits to an AI",
				"Signed-off-by",
				"Co-authored-by",
			} {
				if !strings.Contains(banned, want) {
					t.Errorf("under the ban the prompt is missing %q", want)
				}
			}

			if strings.Contains(banned, "Assisted-by commit trailer") {
				t.Error("under the ban the attribution footer still refers to the Assisted-by trailer")
			}

			unbanned := tc.render("")
			if !strings.Contains(unbanned, "Assisted-by: Claude") {
				t.Error("without a ban the prompt no longer asks for the Assisted-by trailer")
			}
			if strings.Contains(unbanned, "## Attribution footer") && !strings.Contains(banned, "this footer is where the assistance is disclosed") {
				t.Error("under the ban the prompt drops the attribution footer that discloses the assistance")
			}
		})
	}
}
