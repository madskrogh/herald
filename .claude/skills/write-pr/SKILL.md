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
5. **Scale to the stakes.** A trivial change gets one or two plain sentences, Verification and the Review notes. Use the full shape only when something is costly to get wrong.

## Before writing, gather

- The issue or spec: goal, why it matters, stakes and risk appetite, non-goals, and the decisions recorded in its comments.
- Surprises recorded during implementation: anywhere reality didn't match the issue or plan.
- Review findings and their outcomes, with the reason given for each and the rejection review's verdict.
- Evidence for load-bearing claims: tests, file and line, logs, queries.
- What you ran to verify the change, with results, and what you didn't check.
- Each review that ran: its model, its effort if it takes one, and the setup commit it ran on.
- Whether this fixes a problem an earlier PR shipped, and which one.

## Shape

Fill in `.github/pull_request_template.md`. `gh pr create --body` doesn't apply it, so write the filled-in template to a file and pass it with `--body-file`. Link the issue, and if decisions were recorded there, say so; don't repeat them. Delete the template's comments and any section that would be empty. Never pad. Verification and the Review notes always stay: they are the evidence for later changes to the agent setup.

If the PR fixes a problem an earlier PR shipped, replace the template's comment under `Closes #` with `Regression from #<n>`. Otherwise delete the comment.

## Verification

A collapsed section with two lists, never left out:

- **Verified**: what was run, and the result. `make ci` on the pushed commit, integration tests by seam, a Message sent to the running stack and its Delivery observed. Name what was run, not how confident you are.
- **Not verified**: what wasn't checked, and why. No end-to-end run, retry timing checked only with a fake clock. If everything was checked, write "nothing".

The summary line counts the Not verified items, `Verification: 2 not verified`, so a gap shows while the section is collapsed. Write 0 for "nothing".

## The opening and plain explanation

- Come first, before the mechanics and the evidence.
- Five sentences at most. Vagueness shows in five; it hides in twenty.
- If you can't explain it plainly, that is a finding: say so in Surprises.

## Read closely

Each item is a location plus what makes a mistake there costly: it can't be changed after merge, it runs on every request, one outcome has two possible causes. Do not explain the code. The reviewer reads it and judges.

## Evidence and assumptions

- Mark every load-bearing claim **checked** (with a pointer) or **inferred**. A pointer beats a label. "Inferred" is a fine answer.
- List assumptions you know you made. No generic filler ("assumes the database is available").
- The list is never complete. Don't present it as if it were.

## Review notes

**Reviews table.** One row per review that ran (Standards, Spec, Bug, Rejection, and any second round), including those that found nothing, so "found nothing" can be told apart from "didn't run":

- **Model**: the model that ran the review: the `model` the skill spawns it with, or the session's model if it sets none.
- **Effort**: for `/code-review`, the level you passed to it; "unknown" if you passed none, since it then reuses an earlier level. The skills don't set an effort for their sub-agents, on purpose: a pinned level would override one the session turned down to save cost. So a sub-agent on the session's model inherits the session's level: write it with "(session)", for example "medium (session)". A sub-agent on another model runs at that model's default: "default (Sonnet)".
- **Setup**: `git log -1 --format=%h -- .claude CLAUDE.md .github/pull_request_template.md`, run when the review ran, in the checkout the session loaded its setup from. A session started in its worktree loads the worktree's; one that moved into a worktree mid-session keeps the setup of the checkout it started in. If the setup changed between rounds, the rows show different commits.
- **Findings**: the count. The rejection review judges only the rejected findings, so its cell says both numbers: "1 disputed of 10 rejections judged". A rejection review skipped because nothing was rejected still gets a row: "none: no rejections".

Below the table, the Claude Code version (`claude --version`): the built-in `/code-review` changes with it, and the Setup commit can't show that.

**Findings table.** Every finding from the standards, spec and bug reviews. Findings the rejection review disputed come first, whether they were then fixed or settled by the human: they are where the author was wrong and only the second look caught it. Then rejected, then fixed. Within each group, most severe first, with "unstated" findings last. Each has its **Severity**: High, Medium or Low as the review stated it. `/code-review` states none, so rate each of its findings High, Medium or Low yourself and mark the rating "(author)", for example "Medium (author)". Rate no other review's findings: if a standards or spec finding has no severity, write "unstated" and keep those findings in the order the review gave them.

Route rejected findings by the reason given:

- **Justified by how the code works** (a transaction, a library's behaviour, a test's needs): stays in the Review notes.
- **Justified by the issue, the spec, a non-goal or "not needed yet"**: agents can check their reading of the human's words, not the human's intent, however many reviewers agree. So the human is asked if it passes the three gates, even when the rejection review upheld it (see `CLAUDE.md`, "Asking the human"), and the Outcome cites the decision recorded on the issue. If it fails them, it stays in the Review notes, marked "settled by reading the issue", so the reviewer can still pull it up.

## Before publishing, check

- Could someone new to the codebase understand the opening?
- Does the description link the issue and, if decisions were recorded there, say so without repeating them?
- Is every rejection that rests on intent either settled by a decision recorded on the issue or marked "settled by reading the issue"?
- Does every reassuring word have a pointer?
- Does Verification say what wasn't checked, or "nothing"?
- Does the Verification summary count match the Not verified list?
- Does every review that ran have a row, including those with no findings?
- Is anything in the main text that belongs in details?

## After the PR opens

- After each push that addresses review, run `make wait-ci` to wait for CI.
- A commit that addresses review says what was wrong, not only what changed: `Release the key when the insert fails` is a change; `Fix key leak when the insert fails` says what was wrong.
- Every review comment gets a reply: "Fixed in `<sha>`", the answer, or the reason it was rejected.
- When fixes change what the description says, update it, including Verification (and its summary count) and the Review notes.
