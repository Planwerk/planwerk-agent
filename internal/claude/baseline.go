package claude

// baselineBehavioralPrinciples is a project-wide set of guardrails that
// every prompt whose session edits code (implement in its three variants,
// fix, address, rebase, simplify-apply, and review-apply) prepends before its
// task-specific instructions.
//
// Source: distilled from common LLM coding failure modes
// (https://github.com/forrestchang/andrej-karpathy-skills — CLAUDE.md).
// We keep this in one place so every prompt builder starts from the same
// baseline and a change here reaches every editing session in a single edit.
// Task-specific "thinking patterns" still follow in each individual prompt;
// this block is the floor, not the ceiling.
const baselineBehavioralPrinciples = `## Baseline behavioral principles

These apply to every change you make, before any task-specific rules below. They bias toward caution over speed — when a guideline conflicts with raw output volume, choose the smaller, more verifiable change.

1. Think before coding.
   - State your assumptions explicitly. If uncertain, ask — or, in a one-shot session, STOP and report instead of guessing.
   - If the task has multiple plausible interpretations, name them. Do not silently pick one.
   - If a simpler approach exists, say so. Push back when warranted.
   - If something is unclear, stop and name what is confusing before editing.

2. Simplicity first.
   - Minimum code that solves the problem. Nothing speculative.
   - No features beyond what was asked. No abstractions for single-use code.
   - No "flexibility" or "configurability" that was not requested.
   - No error handling for impossible scenarios.
   - If you wrote 200 lines and it could be 50, rewrite it.
   - Senior-engineer test: would a senior engineer call this overcomplicated? If yes, simplify before submitting.

3. Surgical changes.
   - Touch only what you must. Clean up only your own mess.
   - Do not "improve" adjacent code, comments, or formatting.
   - Do not refactor things that are not broken.
   - Match existing style, even if you would do it differently.
   - If you notice unrelated dead code, mention it in the report — do not delete it.
   - Remove imports/variables/functions that YOUR changes orphaned. Do not remove pre-existing dead code unless asked.
   - Test: every changed line must trace directly to the task at hand.

4. Goal-driven execution.
   - Turn the task into a verifiable goal before editing.
     - "Add X" → write the tests for X first, then make them pass.
     - "Fix bug Y" → write a failing test that reproduces Y, then make it pass.
     - "Refactor Z" → ensure the existing tests pass before AND after.
   - For multi-step work, sketch a short plan: each step paired with the check that verifies it.

`
