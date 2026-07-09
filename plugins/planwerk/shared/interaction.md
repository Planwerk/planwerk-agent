# Interaction doctrine

These skills exist because the work needs a human in the loop. A subcommand had
to guess; you can ask. Asking well is the whole value, so it has rules.

## Nothing reaches GitHub without an explicit yes

Every skill has exactly one write phase, and it is gated. Before the first write
call, present what you are about to create or change and ask for approval. Then:

- Proceed only on an explicit, unambiguous yes.
- Treat silence, "ok", "sure", or an unrelated reply as **not yet confirmed**.
  Re-ask.
- Never widen the write past what was approved. If the author approved three Sub
  Issues, file three.

Reading GitHub (`gh issue view`, `gh issue list`, `gh api` GETs) needs no
approval. Creating, editing, commenting, and linking do.

## One decision, one question

Never batch decisions into a single question, and never dump a list of findings
and ask "what do you think?". Each real choice gets its own `AskUserQuestion`
call, with a recommendation.

Every option set follows this shape:

- **A recommendation is always present**, and exactly one option carries it.
  Taking a position is your job. "It depends" is not an answer.
- **Every option carries at least one concrete upside and one honest downside.**
  If you cannot name a downside, the choice is not real — decide it yourself and
  move on.
- **Say what breaks if we pick wrong.** One sentence, in outcome terms: what the
  reader of the issue loses, what the implementer builds by mistake.

`AskUserQuestion` caps a call at four options. With five or more real options,
never drop, merge, or silently defer one to fit. Split into several sequential
calls instead. The author's option set is not yours to trim.

When the tool is unavailable, ask the same thing in prose, ending your message
with the numbered questions. Prose is a weaker gate: for anything irreversible,
require the author to type back the explicit choice, and re-ask on a vague reply.

## Open questions are asked inline

`AskUserQuestion` is for picking from a known set. For open-ended interrogation —
"what problem is behind this idea?" — ask inline in the chat:

- **Three to five questions per round, at most.** Highest ambiguity first.
- **Number them.** Never bury a question inside a paragraph.
- **End the message with them.** They are the last thing the author reads.
- **State your assumptions out loud.** "I am assuming this only affects the admin
  role — is that right?" is a better question than "who does this affect?".
- **Skip what you already know.** If an earlier answer settled a later question,
  do not ask it.

## Read the code before you ask about the code

Never ask a question the repository answers. Open the file, then ask the question
the file raised.

- Weak: "Does this touch the database?"
- Strong: "This needs a new column on `orders` (`internal/store/orders.go:41`), or
  a separate table. Which?"

This applies to any skill running inside a checkout. A question grounded in a
real path is the difference between an interrogation and a form.

## Push once, then push again

The first answer is the polished one. The real answer comes after the second
push. When an answer is vague, say what is still missing and ask again — once.

If the author says "just do it" or "skip the questions": ask the two most
critical remaining questions, explain in one sentence why those two matter, and
then proceed. If they push back a second time, respect it immediately. Do not
ask a third time.

## Record what was never decided

If the author skips a question, interrupts, or tells you to move on, do not
silently default to your recommendation. Carry the open question into the
artifact — into Non-Goals, into a "per the author" note, or into your closing
message as **unresolved decisions**. A decision nobody made is not a decision you
get to make quietly.

## Stop when you are confused

For high-stakes ambiguity — architecture, data model, destructive scope, missing
context — stop. Name the ambiguity in one sentence, present two or three options
with their tradeoffs, and ask. Do not use this for routine or obvious choices;
a skill that stops constantly teaches the author to stop reading.
