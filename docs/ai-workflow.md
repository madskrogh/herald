# Working with coding agents

Herald is built with Claude Code as a pair programmer. This document describes the setup: what the agents are told, what is enforced mechanically, and how several agents work in parallel without stepping on each other. Everything here is committed, so any checkout (a collaborator's machine, a cloud agent, CI) gets the same behaviour.

## Principles

- **Instructions are suggestions; tooling is a guarantee.** Anything that must always happen is a hook, a Make target or a CI check. Prose instructions tell the agent what to do when one of those fails.
- **Rules come from evidence.** Style rules are added when an agent (or a human) gets something wrong in review, not copied in from style guides the model already knows.
- **Context is a budget.** Rules load only for the files they concern, and skills load only when a task calls for them.
- **The repo carries its own setup.** Personal preferences live in the developer's home directory; anything about how this codebase is built lives here.

## What lives where

| Path | Purpose |
|---|---|
| `CLAUDE.md` | Entry point for agents: what Herald is, where the spec is, commands, the workflow |
| `.claude/rules/` | Conventions loaded by file path: Go style (`*.go`), testing (`*_test.go`), SQL and sqlc (`*.sql`) |
| `.claude/skills/` | On-demand procedures: `tdd`, `codebase-design`, `standards-review` (adapted from [mattpocock/skills](https://github.com/mattpocock/skills), MIT), `rejection-review` and `write-pr` |
| `.claude/hooks/` | Scripts run by Claude Code on events |
| `.claude/settings.json` | Sandbox, hooks, worktree settings |
| `scripts/worktree-env.sh` | Per-worktree `.env` (compose project name, ports) |

## The loop for one task

One GitHub issue = one branch = one worktree = one agent session = one PR.

1. **Issue.** The task is a GitHub issue, small enough for one PR. It states the problem and the change in plain words, and names the requirement IDs it touches; the spec text itself is written in the PR.
2. **Start.** `claude -w issue-12` starts a session in worktree `issue-12` on branch `worktree-issue-12`, created from `origin/main`. Naming the worktree after its issue lets a later session find it and resume the work. The agent reads the issue with its comments, where earlier decisions are recorded.
3. **Build test-first** with the `tdd` skill: agree the seams to test and settle the issue's open points with the human first, then red → green in vertical slices. A behaviour change starts in the spec: the requirement and any vectors, then the tests for them, failing, then the code. Each decision is recorded as a comment on the issue.
4. **Design review.** With tests green, `standards-review` checks the diff on two axes: does it follow the documented standards (plus a baseline of code smells), and does it match the issue and the requirements it touches? The second axis also flags every decision in the diff that no requirement states (see "Decisions go in the spec"). The agent refactors on its findings, with the tests as a safety net.
5. **Bug review.** The built-in `/code-review` runs on the final diff. It comes after the refactor so it reviews the code that ships.
6. **Rejection review.** The `rejection-review` skill gives every finding the author rejected to a fresh-context reviewer, which upholds or disputes the stated reason. A disputed rejection is fixed, unless the author has an argument the reviewer didn't address: then the human is asked before the PR opens. So is a rejection that rests on the issue's intent ("not needed yet", a non-goal), even when upheld, if the stakes are real: a reviewer can check the issue's wording, not what the person who wrote it meant. One with low stakes stays in the Review notes, marked "settled by reading the issue". Each answer is recorded as a comment on the issue.
7. **Gate.** `make ci`, then push and open a PR. The `write-pr` skill fills in `.github/pull_request_template.md` for a reviewer who works like a coach: they hold the context and ask the questions, so the description points them where to look rather than arguing for the change. It opens with a plain explanation. It links the issue, where any decisions the human made were recorded, then lists the files to read closely. A **Verification** section says what the author ran, with results, and what they didn't check and why; it is never left out, so "not checked" is stated rather than implied. Verification is collapsed, with the number of unchecked items in its summary line so a gap shows while it is closed. Mechanics, evidence and the **Review notes** fold into collapsed sections. The Review notes have a row for every review that ran, with its model, effort and setup commit, even when it found nothing, and every finding with its severity and how it was resolved. A PR that fixes a problem shipped by an earlier one says `Regression from #<n>`. After each push, the agent waits for CI with `make wait-ci` instead of polling: a poll costs a model turn per check and fills the context with output that says nothing until the result. A hand-written wait tends to watch the wrong run, or none, because the run doesn't exist yet right after a push; the target handles both. On a failure it reads only the failing job's log excerpt.
8. **After the PR opens.** A commit that addresses review says what was wrong, not only what changed, and every review comment gets a reply: "Fixed in `<sha>`", the answer, or the reason it was rejected. That keeps each comment traceable to the commit that settled it.

The two reviews are kept separate on purpose: a design smell and a correctness bug call for different actions, and mixing them in one list buries the bugs.

### Who does what

Roles that need context and roles that need distance go to different agents:

- **Writing and fixing need context**, so the session that wrote the code also addresses the findings, as an author addresses review comments.
- **Reviewing needs distance.** Every review starts with a fresh context and sees the diff, the issue and the documented standards, never the author's reasoning: `standards-review` spawns its sub-agents that way, and `/code-review` runs as a forked skill, which doesn't see the conversation either.
- **Judging a rejection needs distance too.** A wrong fix is caught by tests and later reviews; a wrong rejection is caught by nothing when the author is the only judge. On the PR for issue #2, two of five rejections from the first review were overturned, and only because the bug review happened to raise them again. The rejection review makes that second look deliberate instead of lucky.

## Decisions go in the spec

[ADR 0007](adr/0007-code-regenerated-from-the-spec.md) sets the goal that all of Herald's code could be regenerated from the spec, the shape files, `GLOSSARY.md` and the ADRs. A decision that exists only in the code is lost in a regeneration, and nobody can review it as a decision: a diff shows that a constant changed, not whether the old value was intended. The scenario's limits on handoff lag and on how late a Delivery's first Attempt may come are such decisions: values with no stated reason.

So every change surfaces its decisions into the spec, in the same PR. Four places hold this:

- **The rule** (`CLAUDE.md`, Conventions): every decision someone at Herald's boundaries could notice is stated in the spec, with its reason. That covers decisions the change makes and ones found in code it touches. A value moved out of the code gets its reason; one without a reason is questioned, not copied.
- **The order** (`tdd`): a behaviour change starts with the requirement and any vectors, then the tests for them ([ADR 0013](adr/0013-vectors-are-input-for-tests.md)). A Go test literal for behaviour that no requirement states is a finding, because that is where decisions used to hide.
- **The review** (`standards-review`): the Spec axis reads the requirements the change touches as well as the issue. It flags each noticeable decision in the diff that no requirement states, and each value moved into the spec or a data file without a reason.
- **The record** (`write-pr`): the PR lists the requirement IDs it adds, changes or withdraws, and each decision it surfaced from existing code.

The issue stays what a change is checked against: it says what to change and records the human's decisions. The spec says what Herald must do, and the word "spec" means only the spec.

These catch decisions that look like decisions: a constant, a limit, a format. They miss implicit ones: the order two things happen in, a default nobody chose, how a failure is reported. The regeneration audit is for those.

## The regeneration audit

The audit runs ADR 0007's own test: regenerate Herald from the spec and see what differs. Every difference is either a defect in the regenerated build, or a decision the spec doesn't state.

It runs at the end of each milestone, and after a large change to the spec. Not on every PR: a full regeneration costs too much for that. The first runs right after the spec first covers today's behaviour (#109).

To run it:

1. **Export the inputs.** Pick the commit to audit. Copy only the inputs into a new directory outside any Herald checkout, with `git archive <commit> <paths> | tar -x -C <dir>`: the spec; the shape files, which are the Tenant API's protos (`api/`), the OpenAPI spec (`docs/openapi/`), the golden responses (`internal/tenantapi/testdata/*.golden`), the handoff proto (`proto/`), and the telemetry schema and Postgres column list once each is a file (until then the column list is ADR 0007's table); `GLOSSARY.md` and `docs/adr/`. Run `git init` there, so neither the code nor its history comes along.
2. **Generate.** Start a fresh Claude Code session in that directory, with permission rules that deny reading any Herald checkout and its git directory. Try one such read first, to confirm it is refused. Ask the session to build Herald from the inputs alone, with a `compose.yaml` that uses Herald's service names (fault injection restarts services by name) and takes its ports from the variables a checkout's `.env` sets. Until the spec states how Herald is deployed, the prompt says both.
3. **Check.** In a new Herald worktree at the audited commit, replace `compose.yaml` with the regenerated build's, its build contexts pointing at the regenerated directory. Then run `make up`, `make e2e` and `make scenario-compare`. The harness comes from the checkout, and the stack it runs against is the regenerated build. `scenario-compare` checks the scenario invariants and compares the regenerated build's edge recording with the committed baseline, which is the current build's.
4. **Classify.** Each failing test, broken invariant and difference in the recording is either a defect in the regenerated build or a decision the spec doesn't state. File each missing decision as an issue that names where the two builds differ.
5. **Record.** Write the audit up on its own issue: the commit it ran against; the model, effort, Claude Code version and prompt that generated the build; and every difference with its classification. Across audits, the number of missing decisions shows whether the reviews are catching decisions earlier.

Until the end-to-end tests and the scenario harness stop importing Herald's own code (ADR 0007, Consequences), they decide what to expect with the current build's filter matching, retry schedule, telemetry constants and stores. Until then, the audit judges the regenerated build partly by the current code's logic: a difference there can be the test's coupling, and a match can hide a gap.

## Branches and merging

- **`main` is protected on GitHub** by a ruleset that blocks force-pushes and deletion. Other branches have no rules, so the guarantee comes from the server, not from any one session's permissions.
- **Squash is the only merge method.** Each PR lands on `main` as one commit titled after the PR (`Sign webhooks with Standard Webhooks (#18)`), which passed CI and review as a whole. That keeps `git bisect` and `git revert` working in PR-sized units, and the agent's working commits (test-first slices, review fixes) stay on the PR instead of in `main`'s history.
- **Agent work is credited in git metadata, not in prose.** Commits carry a `Co-authored-by` trailer, and GitHub keeps it on the squash commit, so `main` records which changes were paired with an agent. PR descriptions get no footer (`attribution.pr` in `.claude/settings.json`): they describe Herald, and the squash commit doesn't include them anyway.
- **Branches are updated by merging `main` into them, never by rebasing once pushed.** With squash merges a rebase buys no cleaner history, and force-pushing a branch under review loses the reviewer's "changes since last review" view. No permission rule lets an agent force-push, so a force-push always needs a person's approval.
- **Branch from `main` only.** A branch built on another unmerged branch has to be rebased after that branch is squashed, so a task that depends on another waits for it to merge.

## Quality checks, from fastest to slowest

| When | What | Why there |
|---|---|---|
| Every `.go` file edit | `goimports -w` (PostToolUse hook) | Cheap, local to one file, always safe |
| Before every push | `make ci` | The full gate, run locally so problems never reach the PR |
| Every PR | CI runs `make ci`, plus Docker image builds | The source of truth; `make ci` keeps local and CI identical |
| End of each milestone, and after a large change to the spec | The regeneration audit | Finds the decisions the reviews didn't recognise; too costly for every PR |

Package-wide checks such as `go vet` don't run on every edit: mid-refactor, half-finished code fails them on purpose, and feeding that back to the agent makes it patch code it is about to rewrite.

## Parallel agents with git worktrees

A worktree is a second working directory linked to the same repository, with its own branch checked out. Several agents can each work in their own worktree at the same time; their work meets only when PRs merge. Tasks that run in parallel should touch different packages, which is one reason the package layout matters.

Worktrees isolate files, not the machine. Ports, Docker containers and the NATS queue group are shared, so each worktree gets its own:

- **Compose project.** `COMPOSE_PROJECT_NAME=herald-<name>` gives each worktree its own Postgres, NATS and volumes. Sharing one NATS would let one worktree's dispatcher take another worktree's Deliveries.
- **Ports.** The main checkout is slot 0; each worktree takes the lowest free slot 1–9, and every published port is shifted by `slot × 100`. A slot is free when no other worktree's `.env` holds it and Docker publishes none of its ports, which catches a stack that outlived its worktree. To tell, the script must reach Docker and read every worktree, so picking a slot fails inside the sandbox: run the first `make up` outside it.

`make up` generates `.env` on first use, so this works however the worktree was created, by a person or an agent. Integration tests use testcontainers and are isolated by construction.

Lifecycle:

```
claude -w issue-<n>  →  make up  →  work, commit  →  make ci, push, PR  →  merge  →  exit the session  →  make worktree-rm NAME=issue-<n>
```

A session locks its worktree while it runs, so `make worktree-rm` refuses a worktree whose session is still open. Run it from the main checkout once that session has exited, or choose "remove" when the session asks on exit.

## Carrying this over to a team

- Rules and skills are versioned with the code and reviewed in PRs like any other change, in PRs of their own so they are never buried in a feature diff. Agents propose setup changes; they don't make them in passing.
- Review notes in each PR description keep the evidence: findings that keep coming up become rules, and procedures repeated across PRs become skills. PRs are shared and permanent, so that's where the evidence lives.
- The evidence is recorded when it happens, because it can't be rebuilt later: what the author verified and what they didn't, how severe each finding was, which model, effort and setup produced each review, which commit answered which comment, and which later PR fixed a problem this one shipped (`Regression from #<n>`). Reviewing the setup across PRs then shows which reviews catch real problems, which produce noise, and what escaped them.
- Setup defects that would affect anyone using the checkout (sandbox, hooks, worktrees, skills) are filed as issues labelled `agent-setup`, so they are fixed in the repo rather than worked around in one session.
- Hooks and Make targets work for every agent session and for people too, so the guarantees don't depend on everyone's personal setup.
- Personal working preferences (how a developer likes to be coached, which shortcuts they use) stay in their own `~/.claude/` and never land in the repo.
- Personal and project instructions are concatenated into the agent's context, not merged, so nothing decides between them when they conflict. `CLAUDE.md` therefore states that the project's instructions win, and asks the agent to report a conflict when it hits one. The repo can't see anyone's personal setup, so it has to carry this rule itself.
