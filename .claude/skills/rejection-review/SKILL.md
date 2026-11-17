---
name: rejection-review
description: Have a fresh-context reviewer judge the review findings the author rejected, before the PR is opened. Use after the standards and bug reviews, or when asked to check rejected or deferred findings.
---

The author decides which review findings to fix and which to reject. A wrong fix is caught by tests and later reviews; a wrong rejection is caught by nothing, because the author is judging their own code. This skill gives every rejection a second opinion from a reviewer who never saw the author's reasoning beyond the stated reason.

## Process

### 1. Collect the rejections

Gather every finding rejected (or deferred) after the standards review and the bug review. For each:

- the finding, as the reviewer stated it;
- which review raised it;
- the author's reason for rejecting it, in one or two sentences.

If there are none, stop: write "no rejections" under Review notes.

### 2. Spawn one sub-agent with a fresh context

Give it only:

- the diff command: `git diff origin/main...HEAD`;
- the originating issue with its comments, where decisions are recorded (`gh issue view <n> --comments`);
- the standards sources: `CLAUDE.md`, `.claude/rules/`, `GLOSSARY.md`, `docs/adr/`;
- the rejection list from step 1.

Don't pass the rest of the author's reasoning or the conversation; independence is the point.

The brief: "For each rejection, read the code it concerns and answer **Upheld** (the reason holds) or **Disputed** (it doesn't), with why, citing the code, the issue or a documented standard. Judge the reason, not whether you would have written the code differently. Check any factual claim the reason rests on ("the reference implementation does the same", "this input is never user-controlled") rather than taking it as given. Under 300 words."

### 3. Act on the verdicts

- **Upheld**: the finding stays rejected. If the reason rests on the issue, the spec, a non-goal or "not needed yet", the judge could check the issue's wording, not what the human meant: if it passes the gates in `CLAUDE.md`'s "Asking the human", ask the human before `make ci` and the push.
- **Disputed**: fix it, test-first like any other change. Only if you have an argument the judge didn't address, and it passes the same gates, ask the human instead, with both sides in one line each.

Record each answer as a comment on the issue, as "Asking the human" says. Run `make ci` after any fixes.

### 4. Record it

In the Review notes, each rejected finding's outcome says what the rejection review concluded: "Rejected, upheld: <reason>", "Disputed, fixed: <what changed>", or "Disputed, settled by the human: <decision>". An upheld rejection that rests on the issue's intent adds "settled by the human: <decision>" or "settled by reading the issue". Each decision is the one recorded on the issue.
