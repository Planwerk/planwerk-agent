package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/prompt"
)

// newPromptCmd builds the "prompt" subcommand: deterministically render a
// copy-paste-ready Claude Code prompt from an existing GitHub issue (typically
// an audit finding or an elaborated proposal). No Claude call — pure prompt
// assembly.
func newPromptCmd(deps *runtimeDeps) *cobra.Command {
	var promptCfg prompt.Options
	var mode string

	promptCmd := &cobra.Command{
		Use:   "prompt <issue-ref>",
		Short: "Generate a Claude Code prompt that fixes or implements an issue",
		Long: `Fetch a GitHub issue and emit a copy-paste-ready prompt for Claude
Code (or another AI agent) to fix or implement it.

Mode is auto-detected from the issue body: issues whose body carries the
audit severity marker ("**Severity**:") get the "fix" prompt; everything
else gets the "implement" prompt. Override with --mode.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			promptCfg.IssueRef = args[0]
			switch mode {
			case "auto", "fix", "implement":
			default:
				return fmt.Errorf("unknown mode %q, supported: auto, fix, implement", mode)
			}
			promptCfg.Mode = promptMode(mode)
			opts := promptCfg
			opts.Version = deps.version
			return prompt.Run(os.Stdout, opts)
		},
	}
	promptCmd.Flags().StringVar(&mode, "mode", "auto", "Prompt variant (auto, fix, implement)")

	return promptCmd
}

// promptMode maps the --mode flag value onto the prompt variant; RunE has
// already rejected anything but auto, fix and implement.
func promptMode(mode string) prompt.Mode {
	switch mode {
	case "fix":
		return prompt.ModeFix
	case "implement":
		return prompt.ModeImplement
	}
	return prompt.ModeAuto
}
