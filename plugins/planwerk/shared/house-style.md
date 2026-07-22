# House style

Rules for every artifact the planwerk skills write: issue bodies, descriptions,
motivations, comments, and the report that ends a run.

## Output language

Write every artifact in English, whatever language the input is written in. The
issue, the seed idea, code comments, and the author's answers may be in another
language. Read them faithfully, but never mirror the language back: the artifact
is always English. Quote identifiers, code, paths, and command output verbatim;
translate the surrounding prose.

**The conversation is the exception.** Ask your questions in the language the
author writes in, so they can answer in their own words. Only the artifact is
pinned to English.

## Prose

- Lead with the most important information; never bury it. State the one core
  point in the first sentence.
- Be concrete: name the actual behavior, component, or change — not "improve the
  system" or "various aspects". This rule is subordinate to accuracy: never
  invent a specific (a file path, symbol, or number) just to sound concrete.
  When a specific is genuinely unknown, mark it as an assumption.
- Active voice, present tense. Short, common words ("use", not "utilize"). One
  idea per paragraph, topic sentence first.
- Cut ruthlessly. Delete throat-clearing openers ("It should be noted that", "It
  is worth noting that", "In other words"). If a sentence adds nothing, remove it.
- Never use AI-slop vocabulary: delve, landscape, multifaceted, notably,
  crucial, comprehensive, nuanced, furthermore, underscore, foster, showcase,
  leverage (as a verb), robust (outside its statistical sense), pivotal,
  groundbreaking, shed light on, pave the way.
- Vary sentence length. Do not dress up your own work with adjectives ("critical
  fix", "powerful feature"). Write "This change…", not a bare "This…".

## Quantify, or say you cannot

Numbers, not adjectives. "Several files" is not acceptable when you can count
them. "Improves performance" is not acceptable without a metric and a target. If
you lack the number, say so and say how to get it — never round an adjective up
into a fake specific.

## Be direct

State what is, not what "could be considered".

- Do not write "you might want to consider…" — state what is wrong.
- Do not write "this could potentially cause…" — state what will happen.
- Take a position. If something is wrong, say it is wrong. If it is fine, do not
  mention it at all.

## First line, last line

A reader catching up reads the first and last lines of a report and skims the
rest. Shape every final report or wrap-up so those two lines are enough:

- Open with the outcome: the verdict or result in one sentence, explanation
  after it — never a narration of what you are about to say.
- If anything is left open, end by naming the single next action — one concrete
  command or step, not a list of options.
- No closers, no recaps. Never end with "Let me know if…", "Hope this helps",
  or a paragraph restating what was already said. The artifact ends when its
  last piece of information is written.

## Anti-hallucination

These are mandatory whenever you cite the repository:

- Every file path you cite must exist. If you are not sure, open the directory
  before naming the file.
- Prefer file-only citations when you cannot verify a line number.
- Never invent symbol names, function signatures, or migration numbers. Open the
  file and read them.
- If the issue references something the repo does not yet have, preserve the
  reference exactly as written and mark it as "per the issue" so reviewers know
  it is an assumption.

## How long a file path stays true

Where an artifact lives decides whether it may name files.

- **`draft` and `meta`** write into the tracker. Their output is picked up weeks
  later, after the surrounding code has moved, so a brief pinned to a file path
  rots. Describe the work by its **behavior and the interfaces it touches** —
  what changes for the people who use or call it. Name no source files.
- **`elaborate`** writes a plan that `implement` consumes against the same
  checkout. Path grounding is required: cite concrete files and symbols, and
  verify each one exists before you name it.

## Design vocabulary

When you reason about architecture, use these terms so every issue and plan
speaks the same language:

- **module** — a unit of code whose implementation is hidden behind an interface.
- **interface** — the surface a module exposes: its signatures, contracts, and
  documented behavior, not its internals.
- **depth** — a module's functionality measured against the size of its
  interface. A deep module hides much behind a small interface.
- **seam** — a place where one implementation can be substituted for another.
- **adapter** — the implementation that translates across a seam to an external
  system.
- **leverage** — the functionality a module provides relative to the interface a
  caller must learn. (Noun only.)
- **locality** — keeping the knowledge needed to understand a behavior in one
  place rather than spread across modules.

Do not substitute the looser "component", "service", or "boundary" for module,
interface, or seam. ("System boundary" and "trust boundary" remain fine.)
