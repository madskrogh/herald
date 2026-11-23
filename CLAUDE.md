# Herald

Herald delivers webhooks on behalf of SaaS products: signed, retried, and observable. It is a portfolio project built to production standards, and a working lab for agent-assisted development. How agents are used here is described in [docs/ai-workflow.md](docs/ai-workflow.md).

## Read before changing code

- [The spec](spec/README.md): Herald's numbered requirements, each with its status, its reason and the test vectors the checks read. [ADR 0007](docs/adr/0007-code-regenerated-from-the-spec.md) sets its form and how it changes, and is a rule for every change, whatever area it touches (see "Decisions go in the spec" below).
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
| `make scenario-compare` | Run the scenario against this checkout's stack (start it with `make up`) and compare what its Endpoints received with the committed baseline, reporting each difference by Delivery and field, then check the scenario invariants (`docs/testing.md`). Takes about five minutes, and nothing else may use the stack meanwhile |
| `make scenario-record` | Run the scenario as `make scenario-compare` does and record what its Endpoints received as the new baseline (`internal/scenario/testdata/baseline.json`), from the baseline's seed or `SEED=<n>` |
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
- Issues, PR descriptions, design docs, ADRs and code comments describe Herald, not the tools used to build it: no agent or tool specifics, and nothing about one machine's setup (local paths, defaults, sandbox behaviour). The exceptions are a PR's Review notes, which record the model, effort and setup behind each review; `agent-setup` issues, which describe a defect in the shared setup and may name the tools; and regeneration audit issues, which record the model and setup that generated the build (`docs/ai-workflow.md`). The agent-setup files (`CLAUDE.md`, `.claude/`, `docs/ai-workflow.md`) may name the tools, but still nothing about one machine's setup.

## Workflow

One GitHub issue = one branch = one worktree = one PR.

1. Start the session with `claude -w issue-<n>`. Read the issue with `gh issue view <n> --comments`: decisions are recorded in its comments.
2. Build it test-first with the `tdd` skill. Agree the seams before writing tests, and settle the issue's open points with the human first (see "Asking the human"). A behaviour change follows ADR 0007's "Changing the spec": the requirement and its vectors come first, failing, and only then the tests and the code. The issue names the requirement IDs it touches; the spec text is written in the PR.
3. Tests green: run the `standards-review` skill against `origin/main`, and refactor on its findings.
4. Run the built-in `/code-review` on the final diff for bugs.
5. Run the `rejection-review` skill on every finding from steps 3 and 4 that you rejected. A disputed rejection gets fixed, unless you have an argument the reviewer didn't address: then ask the human, if it passes the gates. So does a rejection that rests on the issue's intent, even when upheld.
6. `make ci`, push, and open a PR. Write its description with the `write-pr` skill, which fills in [.github/pull_request_template.md](.github/pull_request_template.md). Its **Verification** says what you ran and what you didn't check. Its **Review notes** record every review that ran, even one that found nothing, and each finding from steps 3 and 4 with its severity and outcome (fixed, or rejected with the reason, the rejection review's verdict and the decision that settled it). The description links the issue and, if decisions were recorded there, says so; it doesn't repeat them. These are the evidence for later changes to the agent setup; `write-pr` says what goes in them. Its **Requirements** lists the requirement IDs the PR adds, changes or withdraws, and each decision it surfaced from existing code. Once the PR is open, run `make wait-ci` (`write-pr` covers later pushes): it blocks until the run on the pushed commit finishes and exits with its result. Don't write your own wait or polling loop. When it fails, read only the failing job's log excerpt.
7. After the PR opens, follow `write-pr`'s "After the PR opens": a commit that addresses review says what was wrong, and every review comment gets a reply.

Setup defects (sandbox, hooks, worktrees, skills) that would affect anyone using this checkout are filed as issues labelled `agent-setup`. Problems with one machine's setup are not.

PRs are squash-merged; it is the only merge method, so a branch's own history never reaches `main`. To bring a pushed branch up to date, merge `origin/main` into it. Don't rebase or force-push a branch once it is pushed: that loses the reviewer's "changes since last review" view, and a squash leaves nothing for it to clean up.

Agent setup (`CLAUDE.md`, `.claude/rules/`, `.claude/skills/`, `.claude/hooks/`, `.claude/settings.json`, `.github/pull_request_template.md`, `docs/ai-workflow.md`) changes only in its own PR, never inside a feature PR. When a task suggests a new rule or skill, don't make the change: ask the human (see "Asking the human"), and file it as an `agent-setup` issue if they agree.

Worktrees: `claude -w <name>` creates `.claude/worktrees/<name>/` on branch `worktree-<name>` from `origin/main`. Name an issue's worktree `issue-<n>` (`claude -w issue-<n>`), so a later session can find it and resume the work. Each worktree gets its own compose project and ports through `.env` (see `scripts/worktree-env.sh`). Integration tests use testcontainers and need nothing from `.env`.

## Asking the human

Some decisions are the human's, and are settled before the PR opens, not in it. Ask when a point is one of the cases below **and** passes all three gates: hard to reverse, surprising to someone without context, a real trade-off. Everything else is your call.

1. **Open in the issue.** The issue (comments included) leaves it open or never addresses it, and you can't settle it from the issue, the spec, an ADR or `GLOSSARY.md`. This includes a surprise that invalidates the issue's premise: stop and ask, don't work around it. Ask before the first test at the latest, and again whenever a new point appears.
2. **Disputed in review.** `rejection-review` disputes a rejection and you have an argument the judge didn't address, or a rejection rests on the issue's intent. Ask before `make ci` and the push.
3. **A change to the agent setup.** A suggested change to `CLAUDE.md`, `.claude/rules/` or a skill. Ask before `make ci` and the push.

How to ask:

- **Check the stakes first.** State the worst case of each option and when it happens. If every worst case is rare or small, it fails the gates. "Old URL or new URL" for an in-flight Message sounds like a data leak, but the leak needs an outage and a URL change at the same moment.
- **Pose it in the human's terms.** "When Endpoints become editable, should an in-flight Message go to the old URL or the new one?" not "store endpoint_id or copy the URL". If the technical framing hides a product question, ask the product question.
- **One decision per question.** A limit change and a status-code change are two decisions. Ask the costliest to get wrong first.
- **Neutral alternatives.** Each option gets its consequence; none gets a defence. State your lean separately, in one sentence.
- **No default-yes endings.** Never write "approve as is" or anything that turns thinking into a vote.

Wait for the answer before going on. Record each decision as a comment on the issue, with the question or finding it answers. The issue, with its decisions, is what the change is checked against: `standards-review` reads it alongside the requirements the change touches. A point the human chooses to leave open is recorded as "left open, ships as the PR does it".
