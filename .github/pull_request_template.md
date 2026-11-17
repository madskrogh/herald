<!-- 1–2 sentences: what changes, for whom, why it matters. -->

<!-- Plain explanation: at most 5 sentences, no lists, for someone new to the codebase. -->

Closes #

<!-- Decisions the human made while the work was underway are recorded on the issue. Say so; don't repeat them. -->

<!-- If this PR fixes a problem an earlier PR shipped, replace this comment with: Regression from #<n> -->

## Read closely

<!-- Files where a mistake is costly, and what makes it costly. Don't explain the code. -->

- `_file or function_`: _what makes a mistake there costly_

## Surprises

<!-- Where reality didn't match the issue or plan, and what was done about it. -->

-

## Not covered

<!-- Scope deliberately left out. -->

-

## Verification

<!--
Never left out. Verified: what was run, and the result. Not verified: what wasn't checked, and why;
"nothing" if everything was checked.
-->

**Verified**

- _What was run (`make ci` on the pushed commit, integration tests by seam, a Message sent to the running stack): the result_

**Not verified**

- _What wasn't checked, and why_

<details><summary>How it works</summary>

</details>

<details><summary>Assumptions and evidence</summary>

<!-- Each load-bearing claim marked checked (with a pointer) or inferred. Assumptions you know you made. -->

</details>

<details>
<summary>Review notes: K reviews, N findings, F fixed, R rejected, D disputed</summary>

<!--
Reviews: one row per review that ran, including those that found nothing. Effort is the level
passed to /code-review, the inherited level for a sub-agent ("medium (session)"), or "default (<model>)". Setup is the last commit touching .claude/, CLAUDE.md or this template
when the review ran: git log -1 --format=%h -- .claude CLAUDE.md .github/pull_request_template.md
-->

| Review | Model | Effort | Setup | Findings |
|---|---|---|---|---|
| | | | | |

Claude Code version:

<!--
Every finding from the standards, spec and bug reviews. Findings the rejection review disputed
first, then rejected, then fixed; each group ordered by severity, most severe first. Severity is High, Medium or Low as the review
stated it, or "unstated" (kept in the review's order). A rejection says why, and what the rejection review concluded;
deferring to a later issue counts as rejected, with that issue's number.
-->

| Finding | Severity | Review | Outcome |
|---|---|---|---|
| | | | |

</details>
