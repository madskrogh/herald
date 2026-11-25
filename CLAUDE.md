# Herald

Herald delivers webhooks on behalf of SaaS products: signed, retried, and observable. It is a portfolio project built to production standards, and a working lab for agent-assisted development. How agents are used here is described in [docs/ai-workflow.md](docs/ai-workflow.md).

## Read before changing code

- [The spec](spec/README.md): Herald's numbered requirements, each with its status, its reason and, where a misreading is likely, test vectors to write tests from ([ADR 0013](docs/adr/0013-vectors-are-input-for-tests.md)). [ADR 0007](docs/adr/0007-code-regenerated-from-the-spec.md) sets its form and how it changes, and is a rule for every change, whatever area it touches (see "Decisions go in the spec" below).
- [docs/adr/](docs/adr/): settled decisions and the architecture. Don't reopen them in passing; propose a new ADR if one needs to change.
- [README.md](README.md): what Herald is, how to run it, and the stack it is built with. Milestones are GitHub milestones.
- [GLOSSARY.md](GLOSSARY.md): domain language. Use these terms in code, tests, comments and docs, and avoid the listed alternatives.

## Commands

| Command | What it does |
|---|---|
| `make up` | Start the local stack with `docker compose` (generates `.env` on first run) |
| `make down` | Stop the stack and delete its volumes |
| `make ci` | Everything CI runs on a PR: lint, tests with `-race`, buf checks, generated-code check |
| `make wait-ci` | Wait for the CI run on `HEAD`'s pushed commit and exit with its result |
| `make admin ARGS='…'` | Run herald-admin against this checkout's stack, e.g. `ARGS='tenant create --name Acme'` |
| `make e2e` | Run the end-to-end tests against this checkout's stack (start it with `make up`). `RUN=<regexp>` selects tests, e.g. `make e2e RUN=TestSpanIsStoredInClickHouse` |
| `make scenario-compare` | Run the scenario against this checkout's stack (start it with `make up`) and compare what its Endpoints received with the committed baseline, reporting each difference by Delivery and field, and its span counts with `counts.json`, by kind of span; then check the scenario invariants (`docs/testing.md`). Takes about five minutes, and nothing else may use the stack meanwhile |
| `make scenario-record` | Run the scenario as `make scenario-compare` does and record what its Endpoints received, and its span counts, as the new baselines (`internal/scenario/testdata/baseline.json` and `counts.json`), from the traffic file the baseline names (`internal/scenario/testdata/traffic.json`). A change to what Herald sends to Endpoints or to its span counts, or to the traffic file, re-records the baselines in the same PR |
| `make scenario-timing BASE=<checkout>` | Run the scenario on this checkout's stack and on the base checkout's at the same time. It is `make scenario-compare` for this checkout, plus a comparison of its timing with the base build's, each statistic within its margin (`docs/testing.md`). Both stacks must be up (`make up` here and in the base checkout), and nothing else may use either meanwhile. Writes `report.md` with everything it found, and the profile diff, to `OUT=<dir>` (default `scenario-timing/<time>/`). Takes about five minutes |
| `make profile BASE=<checkout>` | Profile this checkout's stack and the base checkout's at the same time, and print how this one's profiles differ. Both stacks must be up (`make up` here and in the base checkout), and get the same traffic while it runs. `make scenario-timing` already takes these profiles; use this one for traffic of your own |
| `make scenario-traffic SEED=<n>` | Print synthetic traffic drawn from a seed, as a draft to start from. The scenario runs only the committed traffic file, which is written by a person or captured from real traffic; this never writes it. How the traffic file and the baseline relate: `docs/testing.md` |
| `make worktree-rm NAME=x` | Stop a worktree's stack, remove the worktree and its branch (after its session has exited) |

Run `make ci` before every push. It is exactly what CI runs.

## Conventions

- This file, `.claude/rules/` and `.claude/skills/` override personal instructions in `~/.claude/`. When they conflict, follow the project's and say so.
- Go conventions are in `.claude/rules/`, loaded automatically by path: `go.md`, `go-testing.md`, `sql.md`.
- Generated code (sqlc, buf) is committed. Never edit it by hand; change the source and regenerate.
- Commit messages: short and imperative (`Add outbox relay`).
- **Decisions go in the spec.** Every decision someone at Herald's boundaries could notice is stated in the spec, with its reason, in the same PR. That covers a limit, a timeout, a count, a format, what happens in a case, the debugging data Herald leaves, and what operators use. It holds for a decision the change makes, and for one you find in code you touch.
  - Moving a value out of the code means finding its reason (the issue, an ADR, the PR that added it), not copying it. If you find none, don't invent one: it is an open point under case 1 of "Asking the human", gates included.
  - When any choice is acceptable, the requirement says MAY.
- **Checks change on their own.** A PR may re-record the committed baselines (`baseline.json`, `counts.json`, `traffic.json`) and add to the span schema, together with the change that moves them. Changing or removing a check, a margin or an invariant goes in a PR of its own: whoever changes behaviour must not also change what judges it. A change that needs both is two PRs, the check first; renaming a span attribute is three. `docs/testing.md` ("Check changes") gives the rule and the order, and `scripts/checks-apart.sh` lists the check files. A CI job runs that script and fails a PR that changes a check file together with anything else; run `scripts/checks-apart.sh origin/main` before pushing.
- Issues, PR descriptions, design docs, ADRs and code comments describe Herald, not the tools used to build it: no agent or tool specifics, and nothing about one machine's setup (local paths, defaults, sandbox behaviour). The exceptions are a PR's Review notes, which record the model, effort and setup behind each review; `agent-setup` issues, which describe a defect in the shared setup and may name the tools; regeneration audit issues, which record the model and setup that generated the build (`docs/ai-workflow.md`); and the case study (`docs/case-study.md`) and the README's account of how Herald is built, which describe the agent workflow (which reviews ran, on which model, what they caught) and may name the tools. The agent-setup files (`CLAUDE.md`, `.claude/`, `docs/ai-workflow.md`) may name the tools too. Nothing in an exception may describe one machine's setup.

## Workflow

One GitHub issue = one branch = one worktree = one PR.

1. Start the session with `claude -w issue-<n>`. Read the issue with `gh issue view <n> --comments`: decisions are recorded in its comments.
2. Build it test-first with the `tdd` skill. Agree the seams before writing tests, and settle the issue's open points with the human first (see "Asking the human"). A behaviour change follows ADR 0007's "Changing the spec", with ADR 0013's change to vectors: the requirement and any vectors come first, then the tests for them, failing, and only then the code. The issue names the requirement IDs it touches; the spec text is written in the PR.
3. Tests green: run the `standards-review` skill against `origin/main`, and refactor on its findings.
4. Run the built-in `/code-review` on the final diff for bugs.
5. Run the `rejection-review` skill on every finding from steps 3 and 4 that you rejected. A disputed rejection gets fixed, unless you have an argument the reviewer didn't address: then ask the human, if it passes the gates. So does a rejection that rests on the issue's intent, even when upheld.
6. `make ci`. Then, once before the PR opens, run the scenario against `origin/main`, the run CI makes against `main`:
   - `git fetch origin`, then `git worktree add --detach <base> origin/main`, where `<base>` is a new temporary directory outside `.claude/worktrees/`. A detached checkout, as CI's, leaves no branch behind, and a session isolated in its worktree can't create a sibling one.
   - `make up` in the base and here, then `make scenario-timing BASE=<base>`. Set `BASE`: its default is the main checkout, which needn't be at `origin/main`. Nothing else may use either stack meanwhile.
   - Its `scenario-timing/<time>/report.md` goes in the PR's Verification (`write-pr`).
   - Remove the base: `make down` in it, then `git worktree remove <base>`.

   Later pushes rely on CI's run. Then push, and open a PR. Write its description with the `write-pr` skill, which fills in [.github/pull_request_template.md](.github/pull_request_template.md). Its **Verification** says what you ran and what you didn't check. Its **Review notes** record every review that ran, even one that found nothing, and each finding from steps 3 and 4 with its severity and outcome (fixed, or rejected with the reason, the rejection review's verdict and the decision that settled it). The description links the issue and, if decisions were recorded there, says so; it doesn't repeat them. These are the evidence for later changes to the agent setup; `write-pr` says what goes in them. Its **Requirements** lists the requirement IDs the PR adds, changes or withdraws, and each decision it surfaced from existing code. Its **Trace** lists the defects it fixes and what the human's questions exposed (see "What the history records"). Once the PR is open, run `make wait-ci` (`write-pr` covers later pushes): it blocks until the run on the pushed commit finishes and exits with its result. Don't write your own wait or polling loop. When it fails, read only the failing job's log excerpt.
7. After the PR opens, follow `write-pr`'s "After the PR opens": a commit that addresses review says what was wrong, and every review comment gets an `Agent:` reply.

Setup defects (sandbox, hooks, worktrees, skills) that would affect anyone using this checkout are filed as issues labelled `agent-setup`. Problems with one machine's setup are not.

PRs are squash-merged; it is the only merge method, so a branch's own history never reaches `main`. To bring a pushed branch up to date, merge `origin/main` into it. Don't rebase or force-push a branch once it is pushed: that loses the reviewer's "changes since last review" view, and a squash leaves nothing for it to clean up.

Agent setup (`CLAUDE.md`, `.claude/rules/`, `.claude/skills/`, `.claude/hooks/`, `.claude/settings.json`, `.github/pull_request_template.md`, `docs/ai-workflow.md`) changes only in its own PR, never inside a feature PR. When a task suggests a new rule or skill, don't make the change: ask the human (see "Asking the human"), and file it as an `agent-setup` issue if they agree.

Worktrees: `claude -w <name>` creates `.claude/worktrees/<name>/` on branch `worktree-<name>` from `origin/main`. Name an issue's worktree `issue-<n>` (`claude -w issue-<n>`), so a later session can find it and resume the work. Each worktree gets its own compose project and ports through `.env` (see `scripts/worktree-env.sh`). Integration tests use testcontainers and need nothing from `.env`.

## Asking the human

Some decisions are the human's, and are settled before the PR opens, not in it. Ask when a point is one of the cases below **and** passes all three gates: hard to reverse, surprising to someone without context, a real trade-off. Everything else is your call.

1. **Open in the issue.** The issue (comments included) leaves it open or never addresses it, and you can't settle it from the issue, the spec, an ADR, `GLOSSARY.md`, or the milestone and deferred-item issues it belongs with. This includes a surprise that invalidates the issue's premise: stop and ask, don't work around it. Ask before the first test at the latest, and again whenever a new point appears.
2. **Disputed in review.** `rejection-review` disputes a rejection and you have an argument the judge didn't address, or a rejection rests on the issue's intent. Ask before `make ci` and the push.
3. **A change to the agent setup.** A suggested change to `CLAUDE.md`, `.claude/rules/` or a skill. Ask before `make ci` and the push.

How to ask:

- **Check the stakes first.** State the worst case of each option and when it happens. If every worst case is rare or small, it fails the gates. "Old URL or new URL" for an in-flight Message sounds like a data leak, but the leak needs an outage and a URL change at the same moment.
- **Pose it in the human's terms.** "When Endpoints become editable, should an in-flight Message go to the old URL or the new one?" not "store endpoint_id or copy the URL". If the technical framing hides a product question, ask the product question.
- **One decision per question.** A limit change and a status-code change are two decisions. Ask the costliest to get wrong first.
- **Neutral alternatives.** Each option gets its consequence; none gets a defence. State your lean separately, in one sentence.
- **No default-yes endings.** Never write "approve as is" or anything that turns thinking into a vote.

Wait for the answer before going on. Record each decision as an `Agent, for the human:` comment on the issue (see "What the history records"), with the question or finding it answers. The issue, with its decisions, is what the change is checked against: `standards-review` reads it alongside the requirements the change touches. A point the human chooses to leave open is recorded as "left open, ships as the PR does it".

## What the history records

The human and the agent post as one GitHub account, and nothing else records who said what, or how a defect got past the steps meant to catch it. Record both as they happen; neither can be rebuilt later.

- **Who posted.** Every comment you post on an issue or PR starts with one of two lines. `Agent:` for your own words: a finding, a question, a report, a reply to a review comment. `Agent, for the human:` for a comment that records the human's decision or answer: it says what they decided and where (the session, a PR comment), and is never written as their own words. A comment with neither line is the human's. A hook refuses every `gh issue comment`, `gh pr comment` and `gh pr review` in a command unless it can read a body that starts with one (a review with no body posts no text, and passes). It reads the command's text, so it also refuses a commit message that quotes such a command; it doesn't see `gh api`.
- **Raised by the human.** When the human's question or objection exposes an assumption the two of you didn't share, or a mistake of yours, comment on the issue as it happens: `Agent:`, then one line headed "Raised by the human": what was asked, what changed, the commit, the requirement ID or "none", and whether it came before the PR or in review. A routine question that changed nothing needs none.
- **Defect trace.** A defect found after the pre-PR reviews (in CI, in the human's review, in telemetry, or in later work) is traced on the issue that reports it, and in full on the PR that fixes it:
  - `Regression from #<n>`: the PR that shipped it.
  - **Got through at:** the earliest step where it could have been seen: the issue, the spec, the tests, the review or the implementation, with a pointer that shows why.
  - **Found by:** CI, the human, the agent or telemetry.
  - **Closed by:** the requirement, test, rule or check that would catch it next time, or "none".

  A missing test counts, as "tests", even when nothing behaved wrongly. A gap the human accepted at merge doesn't: its decision is on its issue.
- **Raised by.** An issue that comes from a question says in its body who raised it and where, for example `Raised by: the human, in PR #<n>`.

The PR's **Trace** lists the defect traces and the "Raised by the human" comments, with links (`write-pr`).
