package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/draft"
)

// newDraftCmd builds the "draft" subcommand: turn a one-line feature idea into
// a ready-to-file GitHub issue through a short Claude-led clarifying Q&A, then
// preview, duplicate-check, and create. It is the front of the pipeline
// (draft -> elaborate -> implement) and deliberately does not elaborate: the
// draft describes the idea, it does not plan it.
func newDraftCmd(deps *runtimeDeps) *cobra.Command {
	var draftCfg cli.DraftConfig

	draftCmd := &cobra.Command{
		Use:   "draft [repo-ref] [idea]",
		Short: "Turn a one-line feature idea into a ready-to-file GitHub issue",
		Long: `Capture a rough, one-line feature idea and draft it into a structured
GitHub issue (a descriptive title plus Description, Motivation, and a rough
Scope) through a short interactive clarification loop. The draft is previewed,
checked for duplicate titles, and created only on explicit confirmation.

This is the front of the pipeline — draft -> elaborate -> implement. It
deliberately does not elaborate: turning the description into a file-level
engineering plan is the separate "elaborate" command.

Without --local, the first positional is the target repository reference
(owner/repo or URL) and the second, optional, is the one-line idea. With
--local the issue is filed against the current checkout's origin remote (no
repo-ref needed) and the single positional is the idea; an explicit ref given
under --local must match origin. When the idea is omitted it is prompted for
interactively. Use --no-interactive/-y to skip the clarifying questions and
draft straight from the seed.

On an interactive terminal the idea and each clarifying answer are captured
in a multi-line composer: Enter starts a new line, Ctrl-D submits, Ctrl-C
cancels, and Ctrl-E opens your editor (honoring $VISUAL, then $EDITOR, then
vi) on the current text. When stdin is piped, stderr is not a terminal, or
--no-interactive is set, draft falls back to single-line reads so scripted
input stays stable.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch draftCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", draftCfg.Format)
			}

			if draftCfg.PrintPrompt && draftCfg.PrintBarePrompt {
				return fmt.Errorf("--print-prompt and --print-bare-prompt are mutually exclusive")
			}

			if err := assignDraftArgs(&draftCfg, args); err != nil {
				return err
			}

			opts := draftCfg.ToDraftOptions(deps.version)
			return draft.Run(cmd.OutOrStdout(), opts, claude.DraftQuestions, claude.Draft, claude.BuildDraftPrompt, claude.BuildBareDraftPrompt)
		},
	}

	draftFlags := draftCmd.Flags()
	draftFlags.BoolVar(&draftCfg.Local, "local", false, "File against the current checkout's origin repo instead of taking an explicit repo-ref")
	draftFlags.BoolVarP(&draftCfg.NoInteractive, "no-interactive", "y", false, "Skip the clarifying Q&A loop and draft straight from the seed idea")
	draftFlags.BoolVar(&draftCfg.DryRun, "dry-run", false, "Render the drafted issue without filing it")
	draftFlags.BoolVar(&draftCfg.NoCreate, "no-create", false, "Alias of --dry-run: render the drafted issue without filing it")
	draftFlags.StringArrayVar(&draftCfg.Labels, "label", nil, "Label to attach to the created issue (repeatable)")
	draftFlags.StringVar(&draftCfg.Format, "format", "markdown", "Output format (markdown, json)")
	draftFlags.BoolVar(&draftCfg.PrintPrompt, "print-prompt", false, "Render the draft prompt for the idea to stdout and exit; do not invoke Claude or GitHub")
	draftFlags.BoolVar(&draftCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained draft prompt to stdout and exit; meant to be pasted into a manual Claude session")

	return draftCmd
}

// assignDraftArgs maps the positional arguments onto the config, resolving the
// [repo-ref] [idea] ambiguity by mode:
//   - print mode: positionals are the idea only (no repo is resolved).
//   - --local: 0 args prompts for the idea, 1 arg is the idea, 2 args are
//     repo-ref then idea (the ref must match origin).
//   - default: the first positional is the required repo-ref, the second the
//     optional idea.
func assignDraftArgs(cfg *cli.DraftConfig, args []string) error {
	switch {
	case cfg.PrintPrompt || cfg.PrintBarePrompt:
		if len(args) > 0 {
			cfg.Seed = args[len(args)-1]
		}
	case cfg.Local:
		switch len(args) {
		case 1:
			cfg.Seed = args[0]
		case 2:
			cfg.RepoRef, cfg.Seed = args[0], args[1]
		}
	default:
		if len(args) == 0 {
			return fmt.Errorf("requires a repository reference argument (or use --local)")
		}
		cfg.RepoRef = args[0]
		if len(args) == 2 {
			cfg.Seed = args[1]
		}
	}
	return nil
}
