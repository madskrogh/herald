# What the workflow catches, and where

Herald is built by coding agents, with a human who decides and merges. This document tests one claim about that workflow against its own history:

> The code the workflow produces holds up. What limits the pace is whether a human understands a change well enough to merge it. The spec and the tests are meant to give that understanding, so that over time review of the PR matters less, and effort moves to the spec and to production observability.

**The short answer:** the agent reviews before a PR opens catch most problems, and little is found wrong after merge. The human's review is recorded mostly on the issues. What it catches is misread intent, the largest case being an ADR with the wrong goal. The history can't show that the code holds up on its own, and it records nothing about how well the human understood a change.

The history read is the 44 PRs merged from [#48][pr48] (the first with Review notes) to [#163][pr163], and the issues they close. "How this was counted" at the end says how each number was got.

## 1. Reviews before the PR catch a lot, and most of it is small

Every PR is reviewed before it opens on three axes: standards and spec (two halves of one design review), and bugs. Then a fresh reviewer judges each finding the author rejected. 37 of the 44 PRs have a findings table, with 750 rows: about 20 per PR.

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

- **High findings are rare, and few are in service code.** 17 of 750, plus one row rated "Medium / High". Both service-code ones are on [#93][pr93]: an Attempt's span error could carry the Endpoint URL, and a missing tracer provider panicked (the "Medium / High" row). The rest are in the scenario's checks ([#101][pr101], [#103][pr103], [#139][pr139], [#140][pr140], [#142][pr142]), the spec, the docs or the agent setup.
- **Lower severity, more rejections:** the author rejected (or rejected, then had disputed) 18% of High, 29% of Medium and 42% of Low.
- **The spec axis fixed the fewest, and changed most over time.** Overall 40% of its findings were fixed, against 68% for standards and 61% for bugs. Up to [#118][pr118] it fixed 28% (45 of 162); from [#132][pr132] on, 62% (54 of 87). Two things changed at that point, so the data can't say which mattered: the spec review moved from Sonnet to Opus 5.5, and the spec it reads came into being ([#134][pr134]).
- **The rejection review rarely disagrees.** It disputed 11 of the 242 rejections it judged. The evaluation after the first milestone (M1) found 5 of about 140 ([#70][iss70]).
- **No fall over time.** The first 18 PRs average 18 rows, the last 19 average 22. This isn't adjusted for the size of the change.

**Not recorded:** what the tests written first caught, since a failing test leaves no row. Overlap between reviews is recorded only where the author merged two findings into one row, so it can't be counted. The spec review ran on Sonnet up to #118. Otherwise only three PRs ran their reviews on Sonnet 5.5 ([#62][pr62], [#65][pr65], [#163][pr163]), too few to compare with Opus 5.5.

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

## 3. The human's review is recorded mostly on the issues, and it catches misread intent

The PRs show almost no review. None has a GitHub review, and only three have comments, six in all, from 16 and 17 November ([#55][pr55], [#56][pr56], [#68][pr68]). The rest of the human's part is on the issues, or in the Review notes. The 39 issues these PRs close have 54 comments, on 25 of them:

- **The largest group is decisions** on points the issue left open: at least 25 comments are headed "Decision", on 19 issues, all but one (#73's, below) posted before the PR opened. For example, a slowed Postgres gets a SHOULD for latency while other faults keep a MUST ([#112][iss112]), and services keep running while NATS or Postgres is down ([#114][iss114]).
- **Five were posted while the PR was open, and the human raised all five in review**, by the comment's own words or as confirmed on this study's issue ([record][rec160]).
  - [#105][iss105], during [#106][pr106]: the ADR's goal, "rewrite Herald in another language", was the wrong one. The human had stated that goal earlier, and the agent took it too literally. What was meant is regeneration, as a test of how well the spec and the tests are defined. The goal became "Herald's code can be regenerated from its spec", and all the spec work since rests on it. #106's description says the reframing was decided "while writing", which is wrong.
  - #105 again, "asked during PR review": the scenario harness imported Herald's own code to decide what to expect. Now it never imports the implementation. #106's description lists this under Surprises.
  - [#73][iss73], during [#99][pr99], "after trying the walkthrough in a worktree before merge": the README's commands failed outside the main checkout. The spec review had raised the hard-coded ports, and the rejection review had upheld the author's rejection. The human's try reversed it. So the agents caught this one, and the rejection step dismissed it.
  - [#108][iss108], during [#117][pr117]: the human asked whether every implemented MUST can have a meaningful vector. The answer kept the rule and left the question to later issues. [ADR 0013](adr/0013-vectors-are-input-for-tests.md) ([#135][iss135]) dropped the rule that evening.
  - [#113][iss113], during [#142][pr142]: the human clarified that morning's decision. The traffic file may also be captured from real traffic.
- **The rest are records**, such as notes carried to a later issue ([#81][iss81] has three) and corrections found while running the work or in its review ([#79][iss79] has four).

Separately, the Review notes of 9 PRs say the human settled 12 findings.

What the five have in common is intent: what the work is for (#105's goal), what it must not depend on (#105's harness), what "works" means to a reader (#73), and what a rule or a decision was meant to say (#108, #113). #105's goal is a misunderstanding the human's own words had started. The agents can check the work against the words, not against what was meant. Five points from 19 to 24 November are too few to show a trend either way.

**Not recorded:** how long the human spent. 27 of the 44 PRs were merged within 30 minutes of opening, CI included, among them [#134][pr134] (about 1,000 lines added) after 6 minutes. But the human may read a branch before its PR opens, and the human and the agent share one GitHub account.

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
- [#64][pr64] told agents to wait for CI with a blocking command. They still wrote polling loops that the setup refused ([#85][iss85]), so [#89][pr89] added a command that does the waiting. A written rule didn't hold; a tool did.

A whole-codebase review of M1 was tried and dropped as too large ([#69][pr69]).

**Not measured:** whether the rule changes worked. The rejection review disputed 2 of 62 rejections before #83 and 9 of 180 after: too few to tell.

## Where the data disagrees with the claim

1. **No sign that review of the PR matters less.** The record of the human's review is too thin to show a trend either way, but one point raised during a PR changed the goal the spec rests on (#105). The agent reviews' findings per PR rose a little.
2. **No baseline for "holds up".** Little was found wrong after the reviews. That is all the data can say.
3. **Understanding isn't measured.** Nothing shows how well the human understood what merged without a comment.

## The regeneration audit

The audit regenerates Herald from the spec alone and sorts each difference: a defect in the regenerated build, a decision the spec doesn't state, or a planned requirement the current build doesn't meet yet ([ADR 0007](adr/0007-code-regenerated-from-the-spec.md)). It is an option, scoped to what changed ([#164][iss164]), and hasn't run. So the claims above about the spec rest on the Review notes and the "Surfaced" lists, not on a regeneration.

## How this was counted

- **Findings** are the rows of each PR's findings table, read by column name. Only rows carry a review, severity and outcome. The summary lines of the same PRs add up to 762, not 750, and disagree with their own tables in 9 PRs, both ways ([#93][pr93]: 26 against 22 rows; [#141][pr141]: 29 against 31).
- **A row two reviews raised** counts once, under the first review named.
- **Outcome:** a row that mentions a dispute counts as disputed, even when it was then fixed. Otherwise the outcome's first words decide: "Fixed", "Partly fixed" or "Addressed" is fixed, and "Rejected" is rejected, even when the human later had it fixed. So the fix rates are slightly low. "Other" is everything else: no change needed, deferred or superseded.
- **Disputes:** 13 rows are marked disputed. The rejection review's own rows report 11 disputes of 242 judged. 289 rows are rejected or disputed, so 47 weren't judged; some PRs say why (cosmetic, or not in scope).
- **Issue comments** are every comment on the 39 issues that the PRs close. A comment counts as raised while the PR was open when its time falls between the closing PR's opening and its merge. Seven do: the five above, a renumbering note on #114, and the agent's question about the flaky check on #81. Who raised a point is taken from the comment's own words, or from the human's answer recorded on #160, since the account is shared.
- **Severity** is the reviewer's own, except the bug review's from [#83][pr83] on, which the author rates. Earlier bug findings say "unstated".

[pr43]: https://github.com/madskrogh/herald/pull/43
[pr48]: https://github.com/madskrogh/herald/pull/48
[pr55]: https://github.com/madskrogh/herald/pull/55
[pr56]: https://github.com/madskrogh/herald/pull/56
[pr62]: https://github.com/madskrogh/herald/pull/62
[pr64]: https://github.com/madskrogh/herald/pull/64
[pr65]: https://github.com/madskrogh/herald/pull/65
[pr68]: https://github.com/madskrogh/herald/pull/68
[pr69]: https://github.com/madskrogh/herald/pull/69
[pr83]: https://github.com/madskrogh/herald/pull/83
[pr89]: https://github.com/madskrogh/herald/pull/89
[pr93]: https://github.com/madskrogh/herald/pull/93
[pr99]: https://github.com/madskrogh/herald/pull/99
[pr101]: https://github.com/madskrogh/herald/pull/101
[pr103]: https://github.com/madskrogh/herald/pull/103
[pr106]: https://github.com/madskrogh/herald/pull/106
[pr117]: https://github.com/madskrogh/herald/pull/117
[pr118]: https://github.com/madskrogh/herald/pull/118
[pr132]: https://github.com/madskrogh/herald/pull/132
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
[iss73]: https://github.com/madskrogh/herald/issues/73
[iss79]: https://github.com/madskrogh/herald/issues/79
[iss81]: https://github.com/madskrogh/herald/issues/81
[iss85]: https://github.com/madskrogh/herald/issues/85
[iss104]: https://github.com/madskrogh/herald/issues/104
[iss105]: https://github.com/madskrogh/herald/issues/105
[iss108]: https://github.com/madskrogh/herald/issues/108
[iss109]: https://github.com/madskrogh/herald/issues/109
[iss113]: https://github.com/madskrogh/herald/issues/113
[iss112]: https://github.com/madskrogh/herald/issues/112
[iss114]: https://github.com/madskrogh/herald/issues/114
[iss135]: https://github.com/madskrogh/herald/issues/135
[iss157]: https://github.com/madskrogh/herald/issues/157
[iss164]: https://github.com/madskrogh/herald/issues/164
[rec160]: https://github.com/madskrogh/herald/issues/160#issuecomment-7156936609
