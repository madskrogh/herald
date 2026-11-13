<!-- 1–2 sentences: what changes, for whom, why it matters. -->

<!-- Plain explanation: at most 5 sentences, no lists, for someone new to the codebase. -->

Closes #

## Decide

<!--
Only what is hard to reverse, surprising without context, and a real trade-off, and only if an
option's worst case is likely or large. Ordered by cost of being wrong. Each item is a question in the reviewer's terms, not an implementation choice.
Options are neutral; the lean is one separate sentence. Work for later goes in an issue, not here.
-->

**1. _The question_**
_Who notices, and when. Reversible or not._ _Lean, in one sentence._

<details><summary>Details</summary>

_The options and the consequence of each; mechanics and reasoning._

</details>

Answer each item in a PR comment ("Decide 1: …"). Anything unanswered at merge ships as this PR does it.

## Read closely

<!-- Files where a mistake is costly, and what makes it costly. Don't explain the code. -->

- `_file or function_`: _what makes a mistake there costly_

## Surprises

<!-- Where reality didn't match the issue or plan, and what was done about it. -->

-

## Not covered

<!-- Scope deliberately left out. -->

-

<details><summary>How it works</summary>

</details>

<details><summary>Assumptions and evidence</summary>

<!-- Each load-bearing claim marked checked (with a pointer) or inferred. Assumptions you know you made. -->

</details>

<details>
<summary>Review notes: N findings, F fixed, R rejected</summary>

<!--
Every finding from the standards, spec and bug reviews. Rejected findings first, then fixed;
each group ordered by severity, most severe first. A rejection says why, and what the rejection
review concluded; deferring to a later issue counts as rejected, with that issue's number.
-->

| Finding | Review | Outcome |
|---|---|---|
| | | |

</details>
