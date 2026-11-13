---
name: write-pr
description: Write or update a pull request description so the human reviewer is invited to think and question rather than skim. Use whenever opening a PR or rewriting its description.
---

# Write a PR for a human coach

The reviewer works like a coach. They hold the context (the business, the users, the risks, the history) and they ask the questions. You hold the technical knowledge. They cannot read everything you wrote, so the PR description decides where their attention goes.

**Easy to read, so the effort goes into thinking.** The reader's attention is the scarce resource. Plain words, short sentences, nothing to decode. Every sentence should either inform a judgement or point to where one is needed. Curiosity comes from clarity and from leaving room for questions, never from making the reader work.

## Principles

1. **Questions about the work come from the human.** Point to where to look, never to what to ask. Do not write questions for the reviewer.
2. **Human terms first.** Goals, users, consequences, in the words of `GLOSSARY.md`. Identifiers, file names, library names and mechanics go in collapsed details.
3. **No reassurance without a pointer.** Never write robust, thorough, safe, comprehensive or fully tested unless evidence the reader can open sits next to it.
4. **Don't argue in the description.** Describe what was done. The case for it goes in the details.
5. **Scale to the stakes.** A trivial change gets one or two plain sentences and the Review notes. Use the full shape only when something is costly to get wrong.

## Before writing, gather

- The issue or spec: goal, why it matters, stakes and risk appetite, non-goals, anything marked undecided.
- Surprises recorded during implementation: anywhere reality didn't match the issue or plan.
- Review findings and their outcomes, with the reason given for each and the rejection review's verdict.
- Evidence for load-bearing claims: tests, file and line, logs, queries.

## Shape

Fill in `.github/pull_request_template.md`. `gh pr create --body` doesn't apply it, so write the filled-in template to a file and pass it with `--body-file`. Delete the template's comments and any section that would be empty. Never pad. The Review notes always stay: they are the evidence for later changes to the agent setup.

## The opening and plain explanation

- Come before Decide, so the reader forms their own view before seeing yours.
- Five sentences at most. Vagueness shows in five; it hides in twenty.
- If you can't explain it plainly, that is a finding: say so in Surprises.

## Decide

A decision goes here only if it passes all three gates: hard to reverse, surprising to someone without context, a real trade-off. Everything else is yours.

- **Pose the question in the human's terms.** "When Endpoints become editable, should an in-flight Message go to the old URL or the new one?" not "store endpoint_id or copy the URL". If the technical framing hides a product question, ask the product question.
- **One decision per item.** A limit change and a status-code change are two decisions.
- **Stakes and reversibility first.** Order items by cost of being wrong.
- **Neutral alternatives.** Each option gets its consequence; none gets a defence. State your lean separately, in one sentence.
- **No default-yes endings.** Never write "approve as is" or anything that turns thinking into a vote.
- **Undecided means Decide.** Anything the issue or spec marks as undecided, or never discussed, is escalated here. Never settle it by reading the issue literally.
- **Changes to your own rules are decisions.** A proposed change to `CLAUDE.md`, `.claude/rules/` or a skill goes at the top of Decide, not the bottom of the PR. If the reviewer agrees, it lands in its own PR.

## Read closely

Each item is a location plus what makes a mistake there costly: it can't be changed after merge, it runs on every request, one outcome has two possible causes. Do not explain the code. The reviewer reads it and judges.

## Evidence and assumptions

- Mark every load-bearing claim **checked** (with a pointer) or **inferred**. A pointer beats a label. "Inferred" is a fine answer.
- List assumptions you know you made. No generic filler ("assumes the database is available").
- The list is never complete. Don't present it as if it were.

## Review findings

Every finding from the standards, spec and bug reviews goes in the Review notes table. Route rejected findings by the reason given:

- **Justified by how the code works** (a transaction, a library's behaviour, a test's needs): stays in the Review notes.
- **Justified by the issue, the spec, a non-goal or "not needed yet"**: becomes a Decide item as well, even when the rejection review upheld it. Agents can check their reading of the human's words, not the human's intent, however many reviewers agree.

## Before publishing, check

- Could someone new to the codebase understand the opening?
- Is every Decide item a question in human terms, with stakes and reversibility?
- Is any rejection that rests on intent still hidden in the review notes?
- Does every reassuring word have a pointer?
- Is anything in the main text that belongs in details?
