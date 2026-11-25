# What the workflow catches, and where

Herald is built by coding agents, with a human who decides and merges. This is a check of one claim about that workflow against its own history.

> The code the workflow produces holds up. What limits the pace is whether a human understands a change well enough to merge it. The spec and the tests are meant to give that understanding, so that over time review of the PR matters less, and effort moves to the spec and to production observability.

The history supports part of it. It does not show the trend the claim predicts, and it cannot answer the "holds up" half cleanly. Both are said below.

## What was read

- The merged PRs from [#48][pr48] to [#163][pr163], as of 25 November 2026: 44 PRs. [#48][pr48] is where PRs began to carry Review notes, so earlier PRs are out.
- 37 of them have a findings table: 750 findings. The other seven are small setup or documentation changes with no review (#53, #57, #64, #74, #90, #143, #156).
- The issues those PRs close, and their comments, where the human's decisions are recorded.

Only what these show is counted. Where they say nothing, the text says "not recorded". Every number is from the PR bodies and issues, counted by script.

What is counted is the rows of each findings table, since only a row carries a finding's review, severity and outcome. The summary lines of the same 37 PRs add up to 762 findings, not 750. They disagree with their own tables in 9 PRs, both ways: [#93][pr93]'s summary says 26 and its table has 22 rows, [#141][pr141]'s says 29 and its table has 31. Some rows hold one finding that two reviews raised. A reviewer's severity is the reviewer's own, except for the bug review, whose findings the author rates from [#83][pr83] on (earlier ones say "unstated").

## Finding 1: three reviews before the PR catch a lot, and most of it is small

Each PR is reviewed three ways before it opens (standards, spec, bug), and a fourth review judges the findings the author rejected. Across the 37 PRs the findings tables hold 750 rows, about 20 per PR. 424 were fixed, 276 rejected, 13 disputed, and 37 ended in no change, a deferral or a replacement.

| Review | Findings | Fixed | Rejected | Disputed | Other |
|---|---|---|---|---|---|
| Standards | 253 | 173 | 74 | 3 | 3 |
| Spec | 249 | 99 | 115 | 4 | 31 |
| Bug (`/code-review`) | 248 | 152 | 87 | 6 | 3 |

A row that two reviews raised is counted once, under the first one named.

| Severity | Findings | Fixed | Rejected | Disputed | Other |
|---|---|---|---|---|---|
| High | 17 | 13 | 3 | 0 | 1 |
| Medium | 184 | 124 | 50 | 4 | 6 |
| Low | 445 | 226 | 184 | 5 | 30 |
| Unstated | 91 | 56 | 32 | 3 | 0 |
| Two severities | 13 | 5 | 7 | 1 | 0 |

"Two severities" is a row that two reviews raised and rated differently, such as "Medium; Low". The lower the severity, the more often the author rejected it: 18% of High, 27% of Medium, 41% of Low.

What stands out:

- **High findings are rare, and mostly not in service code.** 17 of 750. Two are defects in the services themselves, both on [#93][pr93]: an Attempt's span error could carry the Endpoint URL, and a nil tracer provider panicked. A third, on [#58][pr58], was a status code that disagreed with the decision on the issue. The rest are in the scenario's checks ([#101][pr101], [#103][pr103], [#139][pr139], [#140][pr140], [#142][pr142]), in the spec ([#141][pr141]) or in the setup.
- **The Spec review is the noisiest.** 40% of its findings were fixed, against 68% for Standards and 61% for Bug.
- **The rejection review disputes few rejections.** Its rows add up to 11 disputes of 242 rejections judged. The M1 evaluation found 5 of about 140 ([#70][iss70]). It found one wrong rejection that had cost a later fix ([#43][pr43], fixed in [#52][iss52]).
- **The count per PR shows no downward trend.** It runs from 3 rows ([#95][pr95]) to 33 ([#56][pr56]), and the last PR read with a large diff, [#154][pr154], has 27.

Not recorded: which findings more than one review raised, so what each review catches that the others miss is unknown. Also not recorded: what the failing tests written first caught, since a red test leaves no row. Only three PRs ran their reviews on Sonnet 5.5 ([#62][pr62], [#65][pr65], [#163][pr163]): too few to compare with Opus 5.5.

## Finding 2: little reaches `main` broken, and the history can't say who finds the rest

After merge, the history shows four cases, each small:

| Case | What | How it was found |
|---|---|---|
| [#52][iss52] | The relay's claim limit and "more waiting" decisions had no test (from [#43][pr43]). | Not recorded. |
| [#66][iss66] | A Batch made only of Messages the dispatcher rejects is never parked. | Known at merge: the human accepted the gap in [#65][pr65], and it is listed there. Not an escape. |
| [#104][iss104] | A run that dies mid-fault leaves Postgres capped, and the next run hangs. | Not recorded. |
| [#157][iss157] | The "postgres slowed" phase is sometimes not late, so the scenario fails now and then. | By CI, once in about 12 runs, after [#154][pr154] ran the scenario on every PR. Fixed in [#163][pr163]. |

#157 is a flaky check that slipped through M2's own review. The check came in with [#101][pr101]. Its author measured that phase at 4.3 s or more, set the line at 3 s, and listed the flake rate beyond two runs as not verified. None of #101's 26 findings is about that margin. Five runs for [#163][pr163] then saw a first Attempt only 2.2 s late. When CI caught it on #154, the human decided to ship and file it rather than wait ([#81][iss81]).

No PR carries the `Regression from #<n>` marker the template asks for, so there is no count of shipped problems to compare with what the reviews caught.

This does not show that the code holds up. The reviews fixed 424 findings before anyone merged, so the code that merges is the code after that work. There is no sample of code written without the workflow, and none of the first drafts is kept.

## Finding 3: the human's part moved before the PR, and was never large on it

The claim says review of the PR matters less over time. The history shows it was already close to nothing in the period read:

- No PR has a GitHub review. Three have comments, six in all ([#55][pr55], [#56][pr56], [#68][pr68]), all on PRs merged on 16 and 17 November. [#62][pr62], merged on 17 November, moved the human's decisions to before the PR opens. No PR since has a comment.
- The decisions are on the issues: at least 25 comments headed "Decision", on 19 issues, not counting this study's own issue. This is a lower bound, since some decisions are written as a question and its answer under another heading (for example [#112][iss112], [#114][iss114]).

The decisions are choices between acceptable options that the issue left open. Examples:

- [#58][pr58]: the `https` rule returns `400`, not `422`.
- [#112][iss112]: a slowed Postgres gets a SHOULD for latency, while every other fault keeps a MUST.
- [#114][iss114]: services start and keep retrying while NATS or Postgres is down, and logs leave out Endpoint URLs.
- [#81][iss81]: ship a known flaky check, rather than wait or stop blocking.
- [#133][pr133]: the human declined a reason the author offered for ADR 0010, and the ADR now calls it an implementation choice.

Not recorded: whether the human reads a PR before merging it without commenting. The account is also shared with the agent, which writes its replies and records the human's answers under the same name, so the comments don't show who wrote them. Nothing records the human's time, either, so "the limit is understanding" cannot be tested here.

## Finding 4: writing the spec surfaced decisions, some with no reason

Moving rules out of the old design document into the spec ([#134][pr134]) and adding the operator boundary ([#141][pr141]) found decisions that only the code held.

- Six PRs list **38** such items under "Surfaced": 15 in [#134][pr134], 10 in [#141][pr141], 6 in [#138][pr138], 4 in [#139][pr139], 2 in [#133][pr133] and 1 in [#140][pr140]. Examples: the 10% retry jitter, the 4 KiB of response body kept, a NUL byte refused in an Application name, the 60 s lease.
- **12 values had no reason** in the code, the issues or the ADRs ([#109][iss109]). Four of them got "a round number with headroom" as their reason. The human approved a reason for each, and for the page size default that any value would do.
- **Some changes were decided, not copied** ([#114][iss114], [#141][pr141]): a 5-minute idle time with no reason became a MAY, and gRPC got the same connection bounds as REST. A dispatcher that exits when NATS is down at start is now specified (as planned) to keep running.
- **The old document was wrong in places.** Parking was in the code but not the document, so "one Delivery per matching Endpoint" was false for a parked Message. It said retries run "about 24 hours", and the delays add up to about 22.
- The rule can conflict with a decision. [#157][iss157] said a change to `lateAfter` goes through the spec, and the spec and bug reviews of [#163][pr163] asked for it. [#112][iss112] had decided it stays out, as a fact about the test harness, and the PR followed #112.

Not recorded: how many of the 38 would have been found without the rule that every change surfaces its decisions, since no PR before it was read for this.

## Finding 5: the setup changed on evidence, and the effect is mostly unmeasured

Four changes show their evidence:

- [#48][pr48] added the verification record, severities and the review table, because the data for an evaluation "can't be rebuilt afterwards" ([#47][iss47]).
- The M1 evaluation read the Review notes of [#13–#68][iss70] and led to four Go rule changes and a sharper rejection review ([#83][pr83]). Its own replays showed the new sentence for the rejection judge did not catch #43's mistake; the new `RunOnce` rule did, in one run.
- [#62][pr62] moved decisions before the PR, after the agent asked three questions on #50 with no place to record the answers ([#61][iss61]).
- [#89][pr89] added `make wait-ci`, after agents wrote polling loops the guard refused ([#85][iss85]).

An attempt at a whole-codebase review of M1 ([#69][pr69]) was dropped as too large.

Not measured: whether any change worked. No PR was checked against the rules added in [#83][pr83], and the dispute counts before and after ([#70][iss70], this document) overlap in #48 to #68 and are too few to compare.

## Where the data disagrees with the claim

1. **No trend.** The claim expects PR review to matter less over time. The PR review was near zero at the start of the data, and the findings per PR did not fall.
2. **"Holds up" has no baseline.** What merges has been through 20 findings a PR on average. Without code written another way, the data cannot say the workflow's code holds up, only that little was found wrong after the reviews.
3. **Understanding is not counted.** Nothing here measures whether the human understood a change. The decisions on issues are the closest sign, and they are the human's own choices, not a count of confidence.

## The regeneration audit

The audit regenerates Herald from the spec alone and sorts every difference into a defect or a decision the spec lacks ([ADR 0007](adr/0007-code-regenerated-from-the-spec.md)). It is an option, run when it is worth its cost and scoped to what changed ([#164][iss164]). It has not run yet. So this study's claims about the spec rest on the Review notes and the "Surfaced" lists above, not on a regeneration.

## Facts to check

| Fact | Value | Source |
|---|---|---|
| Merged PRs read | 44 (#48 to #163) | `gh pr list` |
| PRs with a findings table | 37 | PR bodies |
| Finding rows | 750 (424 fixed, 276 rejected, 13 disputed, 37 other) | Review notes |
| Findings by summary lines | 762 | Review notes |
| High findings | 17 (13 fixed, 3 rejected, 1 decided in session) | Review notes |
| Disputes by the rejection review | 11 of 242 judged | Review notes |
| GitHub reviews on PRs | 0 | `gh pr view` |
| PR comments | 6, on #55, #56, #68 | `gh pr view` |
| Issue comments headed "Decision" | at least 25, on 19 issues | `gh issue list` |
| Items listed as "Surfaced" | 38, in 6 PRs | PR bodies |
| Values with no reason, given one | 12 | [#109][iss109] |
| PRs with `Regression from #<n>` | 0 | PR bodies |

[pr43]: https://github.com/madskrogh/herald/pull/43
[pr48]: https://github.com/madskrogh/herald/pull/48
[pr55]: https://github.com/madskrogh/herald/pull/55
[pr56]: https://github.com/madskrogh/herald/pull/56
[pr58]: https://github.com/madskrogh/herald/pull/58
[pr62]: https://github.com/madskrogh/herald/pull/62
[pr65]: https://github.com/madskrogh/herald/pull/65
[pr68]: https://github.com/madskrogh/herald/pull/68
[pr69]: https://github.com/madskrogh/herald/pull/69
[pr83]: https://github.com/madskrogh/herald/pull/83
[pr89]: https://github.com/madskrogh/herald/pull/89
[pr93]: https://github.com/madskrogh/herald/pull/93
[pr95]: https://github.com/madskrogh/herald/pull/95
[pr101]: https://github.com/madskrogh/herald/pull/101
[pr103]: https://github.com/madskrogh/herald/pull/103
[pr116]: https://github.com/madskrogh/herald/pull/116
[pr133]: https://github.com/madskrogh/herald/pull/133
[pr134]: https://github.com/madskrogh/herald/pull/134
[pr138]: https://github.com/madskrogh/herald/pull/138
[pr139]: https://github.com/madskrogh/herald/pull/139
[pr140]: https://github.com/madskrogh/herald/pull/140
[pr141]: https://github.com/madskrogh/herald/pull/141
[pr142]: https://github.com/madskrogh/herald/pull/142
[pr154]: https://github.com/madskrogh/herald/pull/154
[pr163]: https://github.com/madskrogh/herald/pull/163
[iss47]: https://github.com/madskrogh/herald/issues/47
[iss52]: https://github.com/madskrogh/herald/issues/52
[iss61]: https://github.com/madskrogh/herald/issues/61
[iss66]: https://github.com/madskrogh/herald/issues/66
[iss70]: https://github.com/madskrogh/herald/issues/70
[iss81]: https://github.com/madskrogh/herald/issues/81
[iss85]: https://github.com/madskrogh/herald/issues/85
[iss104]: https://github.com/madskrogh/herald/issues/104
[iss109]: https://github.com/madskrogh/herald/issues/109
[iss112]: https://github.com/madskrogh/herald/issues/112
[iss114]: https://github.com/madskrogh/herald/issues/114
[iss157]: https://github.com/madskrogh/herald/issues/157
[iss164]: https://github.com/madskrogh/herald/issues/164
