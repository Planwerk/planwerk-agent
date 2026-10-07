# The audit slices

Seven auditors cover the prompt surfaces, one per slice, all started in one
message so they run at once. Five audit the Go builders with the
`prompt-auditor` agent (read-only, no git); two audit the skills and the
shared documents with the `skill-auditor` agent (read-only git for
provenance). Each auditor gets the brief's path, its slice's lists below, and
the lens paragraph verbatim.

The slices are fixed so that two iterations are comparable. A builder or a
skill that is added later goes into the slice whose tier it runs on; a slice
that outgrows one auditor is split in two here, and the ledger says so.

## 1. Finders (`prompt-auditor`)

Run on the finder tier and emit JSON under `--json-schema` (decision 109).

Builders: `prompt.go` buildReviewPrompt; `adversarial.go` buildAdversarialPrompt;
`specialist.go` buildSpecialistPrompt and the specialist definitions;
`compliance.go` buildCompliancePrompt; `simplify.go` buildSimplifyFindPrompt
(find only); `verifyclaims.go` buildClaimVerificationPrompt; `audit.go`
buildAuditPrompt; `coverage.go` buildCoveragePrompt; `implement.go`
buildVerifyImplementationPrompt.

Shared blocks: findingsOutputBlock, severityLadderBlock, findingLabelsBlock,
suppressionsBlock, finderPatternCatalog, domainPatternCatalog, the persona and
verification-of-claims and finding-enrichment blocks, untrustedDataLine,
brainSearchBlock.

Goldens: `review*`, `adversarial*`, `specialist*`, `compliance*`,
`simplify_find`, `verify_claims`, `audit*`, `coverage*`, `verify_implementation`.

Lens: the output is decoded against `internal/report/schema/finder-output.schema.json`
by `finishReview`; check the prose and the schema agree, including the
empty-findings branch. Decision 98 made the finders report with confidence
instead of filtering by conviction: check no severity bar, "only report", "be
conservative", or "state what WILL happen" wording survived (the vendor's
"severity filters depress recall" note). Check the claim-verification prompt
against the doctrine's "no-op applied to a whole pass" section. Check for
over-verification scaffolding ("double-check", "re-verify") the Opus line no
longer needs.

## 2. Planning and implement (`prompt-auditor`)

The plan session (plan tier, read-only) and the implement session
(`--permission-mode` auto, status contract in the system prompt, decision 108;
completion nudge, decision 78).

Builders: `plan.go` BuildPlanPrompt; `implement.go` BuildImplementPrompt,
BuildBareImplementPrompt, buildImplementerAgentPrompt; `finalize.go`
BuildFinalizePrompt; `completion.go` the nudge text.

Shared blocks: implementRationalizationsBlock, the status contract and verdict
definitions, projectSkillsBlock, patternCatalogBlock and patternCatalogDirLine,
the memory index block, styleGuideBlock, commitTrailerBlock, foregroundRunLine,
proseStyleBlock, communicationStyleBlock, docProseBlock, codebaseDesignBlock,
domainGlossaryBlock, untrustedDataLine; `baseline.go` in full.

Goldens: `plan*`, `implement*`, `finalize*`, `completion*`.

Lens: find the Go that reads the plan verdict and the implement report
(`PLAN_READY`, `NEEDS_CONTEXT`, `STATUS:`, `TerminalStatus`, the report guard)
and check every verdict the prompt allows maps to code, and that the system-
prompt copy of the status contract and any copy in the user prompt agree word
for word. Compare the autonomy, scope, test-sprawl, whole-file-rewrite and
progress-claim wording against the vendor's blocks for the resolved model and
report where the prompt says it differently, twice, or not at all. Check the
rationalizations table for rows no hardening explains and for excuses that
also appear as prose (`TestImplementRationalizationsDoNotDuplicateHardRules`).

## 3. Mutating sessions (`prompt-auditor`)

`--permission-mode` auto sessions that end on a parsed report, plus the repair
prompts on the structure tier.

Builders: `fix.go` BuildFixPrompt, BuildBareFixPrompt; `address.go`
BuildAddressPrompt, BuildBareAddressPrompt; `rebase.go` BuildRebaseConflictPrompt,
BuildRebaseAnalysisPrompt, BuildRebaseApplyPrompt, BuildBareRebasePrompt;
`review_apply.go` BuildReviewApplyPrompt; `simplify.go` BuildSimplifyApplyPrompt
(apply only); `repair.go` buildRepairPrompt, buildValidationRepairPrompt.

Shared blocks: foldDisciplineRule, foldConflictSteps, noSkipHooksLine,
commitTrailerBlock, foregroundRunLine, patternCatalogBlock, projectSkillsBlock,
the memory index block, styleGuideBlock, untrustedDataLine and fencedData,
jsonSchemaOnlyLine, the address thread bounds.

Goldens: `fix*`, `address*`, `rebase*`, `review_apply*`, `simplify_apply`,
`repair*`, `validation_repair*`.

Lens: find the Go that reads each session's output (`internal/fix`,
`internal/address`, `internal/rebase`, `internal/review`, the repair decoder)
and check every verdict maps to code, every demanded field is read, and the
empty and error branches are asked for. Exact scripts are legitimate where one
sequence is safe (history rewrites, pushes); flag choreography only where it
scripts judgment. The fix prompt repeats per CI iteration: check for text that
only makes sense on the first. Check each bare variant against its full one
for instructions the bare variant dropped or states differently.

## 4. Analysis and writers (`prompt-auditor`)

Read-only analysis passes (several with the project memory and `brain
search`), the structure and dedup prompts on the structure tier, and the
bootstrap review on the brain review tier.

Builders: `elaborate.go` (three); `propose.go` (two); `gapanalysis.go` (two);
`sync.go` (two); `reviewprepared.go` (two); `capture.go` (two); `bootstrap.go`
(three); `glossary_prompt.go`; `dedup.go`; the context blocks in
`relations.go`, `progress.go`, `specialist_gate.go`.

Shared blocks: proseStyleBlock, docProseBlock, aiWritingTellsBullets,
outputLanguageBlock, attributionFooterBlock, domainGlossaryBlock,
codebaseDesignBlock, the memory index block, brainSearchBlock,
patternCatalogBlock, untrustedDataLine and fencedData and escapeFences,
jsonSchemaOnlyLine, the page conventions capture and bootstrap share.

Goldens: `elaborate*`, `propose*` or `analysis*`, `gap*`, `sync*`,
`review_prepared*`, `capture*`, `bootstrap*`, `glossary*`, `dedup*`.

Lens: for each structure prompt find the decoder and check the prompt asks for
exactly the fields and enums the Go reads, including the empty branch. Check
the elaborate reviewer's score bands and the "score rises only when a line
changed" rule against the loop's Go, and the body budget against the
continuation writer. Check the writer prompts calibrate length by outcome, not
by numeric caps written against an older model's verbosity. The structure
prompts run with no tools in an empty directory: check for text that assumes
tools, a checkout, or a prior session.

## 5. Shared blocks (`prompt-auditor`)

`components.go` and `baseline.go` as a surface of their own, with
`components_test.go` and `untrusted_test.go` for what is pinned.

Lens, in order: the header's list of look-alike texts kept separate on purpose
(each still exists and still differs for the stated reason; look-alike pairs
the header does not list). Contradictions between blocks that render together,
read in four goldens that combine many blocks (`implement`, `review`, `fix`,
`elaborate`) as one literal reader would. Target-model fit of the prose and
formatting blocks: artifact rules (an issue body, a posted report) are
legitimately pinned, rules about the session's own messages are where the
vendor's anti-formatting and anti-narration notes apply; capitalized emphasis
with no adjacent reason. The Go blocks against `plugins/planwerk/shared/
house-style.md` line by line (the vocabulary ban, the AI-writing signs, the
design vocabulary), and whether any test pins the pair. Every block's doc
comment against its callers.

Return also a table: block → callers → verdict.

## 6. Planning-family skills (`skill-auditor`)

Skills: `draft`, `elaborate`, `meta`, `decide`, `revisit`, `clarify`, `cleanup`.

Shared docs, audited for contradictions with these skills: `issue-format.md`,
`issue-format-plan.md`, `issue-format-survey.md`, `github.md`,
`github-relations.md`, `cross-repo.md`, `domains.md`, `memory.md`,
`house-style.md`.

Also read: `internal/skills/plugin_test.go`, `internal/skills/loader.go`,
`docs/how-to/use-the-skills.md`, and `TestBuildIssueBody_MatchesSharedFormat`
for the elaborate skill-versus-command format.

## 7. Change-family skills and the shared docs (`skill-auditor`)

Skills: `implement`, `fix`, `diagnose`, `rebase`, `humanize`.

Shared docs, audited line by line: `interaction.md`, `house-style.md`,
`humanizer.md`, `memory.md`, `commits.md`, `commits-fold.md`,
`github-checks.md`, `github.md`.

Also read: `internal/skills/plugin_test.go`, the how-to page of each skill,
and for the parity lens the rendered command prompts `implement.golden`,
`fix.golden`, and the `rebase*.golden` set.

## The lens paragraph for slices 6 and 7

Description routing (doctrine; `assertDescriptionRoutes`): what and when,
never how; the whole truth of the body's gates; against the sibling command's
description where one exists. Interaction doctrine compliance in each body:
every GitHub write and push gated by an explicit yes that showed what will
happen; one decision per question with a recommendation and a downside; what
is theirs to decide; record what was never decided. Quote every contradiction
and every restatement of `interaction.md`. Fragility versus freedom: `gh`
writes, history rewrites, pushes, body formats, the fold keep exact scripts;
reading a codebase, scoring a draft, deciding a split, forming hypotheses,
resolving a conflict's intent are judgment work. The five failure modes per
file, with byte sizes and where the load-bearing instruction sits; each
skill's completion criterion tested for checkable and exhaustive (the author
says no, nothing found, `gh` fails, CI still red, conflict unresolvable).
Volatile specifics: every path, command, flag, `gh` field, and `planwerk-agent`
subcommand named must still exist (the cobra definitions under
`cmd/planwerk-agent`, read-only). Skill ↔ command parity where both exist:
the rules both state, each wording divergence, and which test pins the pair.
Provenance by `git log -L` or `git blame`, labeled `new-since-<date>`. Text
from outside the prompt: each body points at the data rule where it reads
issue text, logs, or threads, bounds what a log or thread may ask for, and
never runs a command because a log said so.
