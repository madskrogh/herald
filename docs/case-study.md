# What the workflow catches, and where

Herald is built by coding agents, with a human who decides and merges. This document tests one claim about that workflow against its own history:

> The code the workflow produces holds up. What limits the pace is whether a human understands a change well enough to merge it. The spec and the tests are meant to give that understanding, so that over time review of the PR matters less, and effort moves to the spec and to production observability.

**The short answer:** the reviews before a PR opens do most of the catching, and little is found wrong after merge. But the history can't show that the code holds up on its own, it shows no trend towards less review, and it records nothing about whether the human understood a change.

The history read is the 44 PRs merged from [#48][pr48] (the first with Review notes) to [#163][pr163], and the issues they close. "How this was counted" at the end says how each number was got.

## 1. Reviews before the PR catch a lot, and most of it is small

Every PR runs two reviews before it opens: a design review with two axes (standards, and spec) and a bug review. A fourth step has a fresh reviewer judge each finding the author rejected. 37 of the 44 PRs have a findings table, with 750 rows: about 20 per PR.

| Review | Findings | Fixed | Rejected | Disputed | Other |
|---|---|---|---|---|---|
| Standards | 253 | 173 | 74 | 3 | 3 |
| Spec | 249 | 99 | 115 | 4 | 31 |
| Bug (`/code-review`) | 248 | 152 | 87 | 6 | 3 |

| Severity | Findings | Fixed | Rejected | Disputed | Other |
|---|---|---|---|---|---|
| High | 17 | 13 | 3 | 0 | 1 |
| Medium | 184 | 124 | 50 | 4 | 6 |
| Low | 445 | 226 | 184 | 5 | 30 |
| Unstated | 91 | 56 | 32 | 3 | 0 |
| Two severities | 13 | 5 | 7 | 1 | 0 |

- **High findings are rare, and only one is in service code.** 17 of 750. The one is on [#93][pr93]: an Attempt's span error could carry the Endpoint URL. The rest are in the scenario's checks ([#101][pr101], [#103][pr103], [#139][pr139], [#140][pr140], [#142][pr142]), the spec, the docs or the agent setup.
- **Lower severity, more rejections:** the author rejected 18% of High, 27% of Medium and 41% of Low.
- **The spec axis is the noisiest.** 40% of its findings were fixed, against 68% for standards and 61% for bugs.
- **The rejection review rarely disagrees.** It disputed 11 of the 242 rejections it judged. The evaluation after the first milestone (M1) found 5 of about 140 ([#70][iss70]).
- **No fall over time.** The first 18 PRs average 18 rows, the last 19 average 22. This isn't adjusted for the size of the change.

**Not recorded:** what the tests written first caught, since a failing test leaves no row. Overlap between reviews is recorded only where the author merged two findings into one row, so it can't be counted. Only three PRs ran their reviews on Sonnet 5.5 ([#62][pr62], [#65][pr65], [#163][pr163]), too few to compare with Opus 5.5.

## 2. Little is found wrong after merge, and one flaky check slipped through

| Issue | What | How it was found |
|---|---|---|
| [#52][iss52] | Two decisions in the relay had no test (from [#43][pr43]). | Not recorded. |
| [#66][iss66] | A Batch made only of bad Messages is never parked. | Known at merge: the human accepted the gap in [#65][pr65]. |
| [#104][iss104] | A scenario run that dies mid-fault leaves Postgres slowed, and the next run hangs. | Not recorded. |
| [#157][iss157] | The scenario fails now and then: its "postgres slowed" phase isn't always late. | CI, once in about 12 runs, after [#154][pr154] ran the scenario on every PR. Fixed in [#163][pr163]. |

**#157 slipped through review.** The check came in with [#101][pr101]. Its author measured the phase at 4.3 s or more, drew the line at 3 s, and listed "the flake rate beyond two runs" as not verified. None of #101's 26 findings is about that margin. The failing CI run had all three Messages under 3 s. When it showed up on #154, the human chose to ship and file it ([#81][iss81]).

No PR carries the `Regression from #<n>` marker the PR template asks for. These four were found by reading the titles of all issues, so a defect fixed without an issue, or under a title that doesn't show it, would be missed.

**This doesn't show that the code holds up.** The code that merges is the code after 424 fixes. Nothing written another way is there to compare with, and first drafts aren't kept.

## 3. The human's part is in decisions before the PR, not on the PR

- **On GitHub, the PRs show almost no review.** None has a review. Three have comments, six in all ([#55][pr55], [#56][pr56], [#68][pr68]), all from before [#62][pr62] moved decisions to before the PR opens. None since.
- **The decisions are on the issues:** at least 25 comments headed "Decision", on 19 issues. Some are written under other headings ([#112][iss112], [#114][iss114]), so this is a floor.
- **12 review findings were settled by the human,** on 9 PRs: their outcome says so, or links the human's decision. All but one had been rejected first.

The decisions choose between acceptable options the issue left open. For example, a slowed Postgres gets a SHOULD for latency while other faults keep a MUST ([#112][iss112]). Services keep running while NATS or Postgres is down, and logs leave out Endpoint URLs ([#114][iss114]). A known flaky check ships rather than blocking every PR ([#81][iss81]).

**Not recorded:** whether the human reads a PR before merging it. The human and the agent share one GitHub account, so comments don't show who wrote them. Nothing records the human's time. So "not visible on the PR" is all this shows, not "no review".

## 4. Writing the spec surfaced decisions, some with no reason

When the rules moved from `docs/design.md` into the spec ([#134][pr134]), and when the operator requirements were added ([#141][pr141]), decisions that only the code held came out:

- **38 items listed as "Surfaced"**, in six PRs: 15 in [#134][pr134], 10 in [#141][pr141], 6 in [#138][pr138], 4 in [#139][pr139], 2 in [#133][pr133], 1 in [#140][pr140]. For example the 10% retry jitter, the 4 KiB of response body kept, and the 60 s lease.
- **12 values had no reason** anywhere ([#109][iss109]). The human approved one for each. For four, the reason is "a round number with headroom".
- **Some were changed, not copied** ([#114][iss114], [#141][pr141]). A 5-minute idle time with no reason became "any value" (a MAY). gRPC got the same connection limits as REST.
- **The old document was wrong in places.** Parking wasn't in it, so "one Delivery per matching Endpoint" was false for a parked Message. It said retries run about 24 hours. The delays add up to about 22.

**Not recorded:** how many of the 38 would have been found without the rule that every change states its decisions in the spec.

## 5. The setup changed on evidence; whether the changes worked is unmeasured

- [#48][pr48] started recording severities, the reviews that ran and what was verified, because that data "can't be rebuilt afterwards" ([#47][iss47]).
- The M1 evaluation ([#70][iss70], [#83][pr83]) read the Review notes of PRs #13 to #68. It tightened four test and code rules, and asked the rejection judge whether a rule's purpose is met. Replays showed the new question didn't catch the mistake that prompted it; one of the new test rules did, in one run.
- [#62][pr62] moved the human's decisions to before the PR, after questions on #50 had no place to be recorded ([#61][iss61]).
- [#89][pr89] added a blocking wait for CI, after agents wrote polling loops that the setup refused ([#85][iss85]).

A whole-codebase review of M1 was tried and dropped as too large ([#69][pr69]).

**Not measured:** whether any change worked. The dispute counts before and after #83 overlap (#48 to #68) and are too small to compare.

## Where the data disagrees with the claim

1. **No trend.** The claim expects review of the PR to matter less over time. GitHub shows almost none from the start, and the findings per PR rose a little.
2. **No baseline for "holds up".** Little was found wrong after the reviews. That is all the data can say.
3. **Understanding isn't measured.** The decisions on issues are the closest sign, and they show choices, not confidence.

## The regeneration audit

The audit regenerates Herald from the spec alone and sorts each difference: a defect in the regenerated build, a decision the spec doesn't state, or a planned requirement the current build doesn't meet yet ([ADR 0007](adr/0007-code-regenerated-from-the-spec.md)). It is an option, scoped to what changed ([#164][iss164]), and hasn't run. So the claims above about the spec rest on the Review notes and the "Surfaced" lists, not on a regeneration.

## How this was counted

- **Findings** are the rows of each PR's findings table, read by column name. Only rows carry a review, severity and outcome. The summary lines of the same PRs add up to 762, not 750, and disagree with their own tables in 9 PRs, both ways ([#93][pr93]: 26 against 22 rows; [#141][pr141]: 29 against 31).
- **A row two reviews raised** counts once, under the first review named.
- **Outcome:** a row marked disputed counts as disputed even when it was then fixed, so the fix rates are slightly low. "Other" is no change needed, deferred or superseded.
- **Disputes:** 13 rows are marked disputed. The rejection review's own rows report 11 disputes of 242 judged. 289 rows are rejected or disputed, so 47 weren't judged; some PRs say why (cosmetic, or not in scope).
- **Severity** is the reviewer's own, except the bug review's from [#83][pr83] on, which the author rates. Earlier bug findings say "unstated".

[pr43]: https://github.com/madskrogh/herald/pull/43
[pr48]: https://github.com/madskrogh/herald/pull/48
[pr55]: https://github.com/madskrogh/herald/pull/55
[pr56]: https://github.com/madskrogh/herald/pull/56
[pr62]: https://github.com/madskrogh/herald/pull/62
[pr65]: https://github.com/madskrogh/herald/pull/65
[pr68]: https://github.com/madskrogh/herald/pull/68
[pr69]: https://github.com/madskrogh/herald/pull/69
[pr83]: https://github.com/madskrogh/herald/pull/83
[pr89]: https://github.com/madskrogh/herald/pull/89
[pr93]: https://github.com/madskrogh/herald/pull/93
[pr101]: https://github.com/madskrogh/herald/pull/101
[pr103]: https://github.com/madskrogh/herald/pull/103
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
