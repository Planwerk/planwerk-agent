# Concept & architecture

planwerk-agent is an AI-powered code review and codebase analysis tool for
GitHub repositories. It uses Claude Code to automatically analyze PR changes
and produce structured review results, to analyze entire repositories and
generate actionable feature proposals, to audit an entire codebase against all
known review patterns, to elaborate high-level issues into detailed engineering
plans, or to generate copy-paste-ready prompts that fix or implement an issue.

## Overview

Each subcommand follows the same shape: a GitHub input is resolved and checked
out locally, review patterns and (where relevant) a SHA-based cache are
consulted, Claude Code performs the analysis, and the result is structured and
rendered. The diagrams below show the data flow for each workflow.

```text
Review:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub PR   │────▶│  planwerk-agent │────▶│  Claude Code  │────▶│  Markdown    │
│  (URL/Ref)   │     │                  │     │  review       │     │  Report      │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                                              │
                            ▼                                              ├──▶ stdout
                     ┌──────────────────┐                                  ├──▶ PR comment (--post-review)
                     │ Review Patterns  │                                  └──▶ Inline review (--inline)
                     │ (local + repo)   │
                     └──────────────────┘

Propose:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub Repo │────▶│  planwerk-agent │────▶│  Claude Code  │────▶│  Proposals   │
│  (URL/Ref)   │     │  propose         │     │  (analysis)   │     │  (MD/JSON)   │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │                      │
                            ▼                        ▼                      ▼
                     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
                     │ Cache (SHA-based)│     │  Structure    │     │ --create-    │
                     │                  │     │  into JSON    │     │ issues (gh)  │
                     └──────────────────┘     └───────────────┘     └──────────────┘

Audit:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub Repo │────▶│  planwerk-agent │────▶│  Claude Code  │────▶│  Findings    │
│  (URL/Ref)   │     │  audit           │     │  (full scan)  │     │  (MD/JSON)   │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │
                            ▼                        ▼
                     ┌──────────────────┐     ┌───────────────┐
                     │ Review Patterns  │     │ Structure into│
                     │ (local + repo)   │     │ BLOCKING/…/   │
                     │                  │     │ INFO findings │
                     └──────────────────┘     └───────────────┘

Elaborate:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub      │────▶│  planwerk-agent │────▶│  Claude Code  │────▶│  Detailed    │
│  Issue       │     │  elaborate       │     │  (repo walk)  │     │  Issue Body  │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │                      │
                            ▼                        ▼                      ▼
                     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
                     │ Cache (SHA+body) │     │  Structure    │     │ --update-    │
                     │                  │     │  into JSON    │     │ issue (gh)   │
                     └──────────────────┘     └───────────────┘     └──────────────┘

Prompt:
┌──────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  GitHub      │────▶│  planwerk-agent │────▶│  Claude Code     │
│  Issue       │     │  prompt          │     │  prompt (stdout) │
└──────────────┘     └──────────────────┘     └──────────────────┘
                            │
                            ▼
                     ┌──────────────────┐
                     │ Auto-mode by     │
                     │ severity marker  │
                     └──────────────────┘
```

For the task-oriented steps behind each workflow, see the
[How-to guides](/how-to/); for the methodology Claude applies during a review,
see [Review methodology](/explanation/review-methodology).
