# Humanizer: the signs of AI writing, and their repairs

The catalog below names the patterns that mark prose as machine-written and
gives the repair for each. It is the full ruleset behind the compact "Signs of
AI writing" section in `house-style.md`: every artifact-writing skill honors
that compact section, and `/planwerk:humanize` applies this full catalog when it
rewrites an existing document. The unattended sessions carry the same compact
rules in their prompts; a Go test keeps the three surfaces from drifting.

Adapted from [blader/humanizer](https://github.com/blader/humanizer) v2.9.1
(MIT), itself based on Wikipedia's "Signs of AI writing" guide, maintained by
WikiProject AI Cleanup. Curated for the artifacts this plugin writes and edits:
issue bodies, plans, documentation pages, doc comments, code comments, and
reports. The upstream sections on injecting personality and on cleaning up
pasted chatbot correspondence are deliberately absent: planwerk artifacts are
technical reference prose, where neutral and plain is the correct human voice.

## Precedence and scope

- A target repository's committed `STYLE_GUIDE.md` outranks every rule here.
  If the repo's guide mandates em dashes, emojis, or title-cased headings, the
  repo wins. Check for the guide before rewriting anything.
- A report format that mandates an em dash in its lead line stands; the em dash
  rule below governs prose, not format contracts.
- Rewrite form only, never meaning. Every fact, name, number, date, and
  citation of the original survives the rewrite, and the rewrite contains no
  fact that is not in the source. Swapping a vague claim for a specific one is
  allowed only when the specific comes from the source; when a sentence needs a
  real-world detail to work, write the plain version without it.

## Content patterns

**Inflated significance.** Watch for: "marks a pivotal moment", "represents a
shift", "reflects broader trends", "underscores its importance", "setting the
stage for", "a testament to". LLM writing puffs up importance by claiming that
arbitrary facts contribute to a broader story. State the fact and stop.

> Before: The loader was established in 2024, marking a pivotal moment in the
> project's evolution toward hermetic sessions.
> After: The loader was added in 2024 as part of making sessions hermetic.

**Fake depth via "-ing" clauses.** Watch for sentence tails like ", highlighting
the need for X", ", ensuring Y", ", showcasing Z". These participle clauses add
ceremony, not information. If the clause carries a fact, make it a sentence; if
not, delete it.

**Promotional language.** Watch for: "boasts", "vibrant", "rich" (figurative),
"seamless", "powerful", "stunning", "comprehensive". Reference prose describes;
it does not sell.

**Vague attributions.** Watch for: "experts argue", "industry reports",
"observers have noted", "it is widely believed". Name the source or cut the
claim; an unsupported claim gets cut, not decorated.

**Formulaic outlook sections.** Watch for: "Despite these challenges...",
"Future outlook", a closing section that restates difficulties and then
reassures. State the known problems as facts and end there.

**Speculative gap-filling.** When a fact is unknown, say it is unknown or cut
the sentence. Never write a paragraph about the absence of information and then
fill the gap with a plausible guess ("likely", "it is believed that", "details
are scarce, suggesting...").

## Language and grammar

**AI vocabulary.** Never use: additionally, crucial, comprehensive, delve,
enduring, enhance, foster, furthermore, garner, groundbreaking, interplay,
intricate, landscape (as an abstract noun), multifaceted, notably, nuanced,
pivotal, showcase, tapestry, a testament to, underscore (as a verb), vibrant,
leverage (as a verb), robust (outside its statistical sense), shed light on,
pave the way. These words are not wrong individually; they cluster in machine
prose, and each has a plainer neighbor. The ban governs prose, never
identifiers, quoted code, or existing API names cited verbatim.

**Copula avoidance.** Write "is", "has", "does". Machine prose dodges the plain
copula with "serves as", "stands as", "acts as", "boasts", "features",
"offers", "represents" where "is" or "has" is meant.

> Before: The gallery serves as the exhibition space and boasts four rooms.
> After: The gallery is the exhibition space and has four rooms.

**Negative parallelisms and tailing negations.** Watch for: "not only ... but",
"it's not just X, it's Y", and clipped tails like "no guessing" or "no wasted
motion" stuck onto a sentence. Say the positive claim as a real clause.

**Rule of three.** Machine prose forces ideas into triads to appear complete
("innovation, inspiration, and industry insights"). List what is true; two
items may stay two, and four may stay four.

**Synonym cycling.** Repetition penalties make models rotate synonyms ("the
loader ... the component ... the module ... the mechanism" for one thing). Call
a thing by one name and repeat it.

**False ranges.** Watch for "from X to Y" where X and Y sit on no meaningful
scale ("from the Big Bang to dark matter"). Name the items instead.

**Hidden actors and subjectless fragments.** Watch for "No configuration
needed." and "The results are preserved automatically." Prefer the active
sentence that names the actor: "You do not need a configuration file. The
system preserves the results."

## Style

**Em dashes and en dashes.** The em dash is among the most reliable AI tells.
Avoid em dashes and en dashes in artifact prose; replace each, in order of
preference, with a period, a comma, a colon, or parentheses, or restructure the
sentence. Also catch spaced hyphens and double hyphens used the same way.
Before finishing a rewrite, scan for `—` and `–`; a hit means the draft is not
done. Exceptions: a report format's mandated lead-line em dash, and a target
repo style guide that endorses them.

**Boldface and decoration.** No mechanical bolding of key phrases, no emoji on
headings or bullets, no bold-fronted bullet lists ("**Performance:** improved
..."). When a bold-fronted list carries real content, either promote the
fronts to plain prose or drop them.

**Headings.** Sentence case, not Title Case. Never follow a heading with a
one-line paragraph that restates the heading before the content starts.

**Quotation marks.** Straight quotes (`"`), not curly ones, in artifacts that
live next to code.

## Filler and structure

**Filler phrases.** "In order to" is "to"; "due to the fact that" is
"because"; "at this point in time" is "now"; "has the ability to" is "can";
"it is important to note that the data shows" is "the data shows".

**Hedging stacks.** One qualifier is a position; three are noise. "It could
potentially possibly be argued that X might" is "X may".

**Generic positive conclusions.** Watch for: "The future looks bright", "a
major step forward", "exciting times ahead". Cut the paragraph and end on the
last concrete fact.

**Signposting.** Watch for: "Let's dive in", "Here's what you need to know",
"Now let's look at". Do the thing instead of announcing it.

**Persuasive authority tropes and aphorisms.** Watch for: "The real question
is", "at its core", "what really matters", and formulas like "X is the Y of
Z". The sentence that follows usually restates an ordinary point with
ceremony. Write the ordinary point.

**Manufactured staccato drama.** One short sentence lands a point. A run of
clipped fragments ("No aesthetic prior. No nostalgia. The old rules were
gone.") is engineered drama; merge them into real sentences.

**Diff-anchored writing.** Documentation and comments describe the thing as it
is, not the change that produced it: no "this function was added to replace
...", no "previously/now" narration outside CHANGELOG entries and migration
notes. A doc comment must read correctly to someone who never saw the diff.

## What not to flag

A human writer can hit several of these patterns without any machine involved.
Look for clusters, not isolated hits: a single em dash means nothing, while em
dashes plus a forced triad plus "vibrant tapestry" plus a generic conclusion is
a confession. Do not flatten formal vocabulary that is not on the banned list,
do not rewrite watched phrases inside quotations or code, and do not touch
specific, hard-to-fabricate detail, mixed feelings, genuine asides, or varied
sentence rhythm: those are the signs of a person, and over-editing destroys
them.

## The rewrite loop

1. Read the input and list every pattern instance you find.
2. Draft the rewrite. Prose only: leave code blocks, identifiers, frontmatter,
   data, and link targets byte-identical.
3. Audit the draft with two questions: "What still reads machine-written?" and
   "Does the draft state any fact, name, number, date, or citation that is not
   in the source?" A fabrication is a defect even when it sounds more human
   than the vague original.
4. Revise into the final text and scan it once more for em and en dashes and
   the banned vocabulary.
