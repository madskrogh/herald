<!-- 1–2 sentences: what changes, for whom, why it matters. -->

<!-- Plain explanation: at most 5 sentences, no lists, for someone new to the codebase. -->

Closes #

<!-- If decisions were recorded on the issue, replace this comment with a line saying so; don't repeat them. Otherwise delete it. -->

## Requirements

<!--
Never left out. Each requirement ID this PR adds, changes or withdraws, and what changed. Then each
decision surfaced from existing code: the value or behaviour, where it was, the requirement that now
states it. "None" if the PR touches no requirement.
-->

- _ID_: _added, changed or withdrawn, and what_
- Surfaced: _value or behaviour_ from `_file_`, now _ID_

## Read closely

<!-- Files where a mistake is costly, and what makes it costly. Don't explain the code. -->

- `_file or function_`: _what makes a mistake there costly_

## Surprises

<!-- Where reality didn't match the issue or plan, and what was done about it. -->

-

## Not covered

<!-- Scope deliberately left out. -->

-

<details><summary>Verification: N not verified</summary>

<!--
Never left out. Verified: what was run, and the result. Not verified: what wasn't checked, and why;
"nothing" if everything was checked. N in the summary counts the Not verified items; 0 for "nothing".
-->

**Verified**

- _What was run (`make ci` on the pushed commit, integration tests by seam, a Message sent to the running stack): the result_
- `make scenario-timing` against `origin/main`: _behaviour comparison ("no differences", or each difference and the requirement that explains it), invariants, timing; a profile diff only when latency or lag differed or the PR claims a performance effect_

**Not verified**

- _What wasn't checked, and why_

</details>

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
first, then rejected, then fixed; each group ordered by severity, most severe first, "unstated" last. Severity is High, Medium or Low as the review
stated it. The author rates each `/code-review` finding, marked "(author)": "Medium (author)". A standards or
spec finding without one is "unstated" (kept in the review's order). A rejection says why, and what the rejection review concluded;
deferring to a later issue counts as rejected, with that issue's number.
-->

| Finding | Severity | Review | Outcome |
|---|---|---|---|
| | | | |

</details>

<details>
<summary>Trace: # defects, # raised by the human</summary>

<!--
Never left out. The fields are defined in CLAUDE.md, "What the history records". Defects: each defect
this PR fixes that was found after the pre-PR reviews. Raised by the human: each "Raised by the human"
comment on the issue, before the PR and in review. "None" under a heading with nothing to list; 0 in
the summary.
-->

**Defects**

- Regression from #_n_. Got through at: _issue, spec, tests, review or implementation_ (_pointer_). Found by: _CI, the human, the agent or telemetry_. Closed by: _requirement, test, rule or check_, or none.

**Raised by the human**

- [_what was asked_](_comment link_): _what changed_, `_commit_`, _requirement ID or none_, _before the PR or in review_

</details>
