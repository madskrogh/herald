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
| `.claude/skills/` | On-demand procedures: `tdd`, `codebase-design`, `standards-review` (adapted from [mattpocock/skills](https://github.com/mattpocock/skills), MIT), `rejection-review`, `write-pr` and `implement-issue` |
| `.claude/hooks/` | Scripts run by Claude Code on events |
| `.claude/settings.json` | Sandbox, hooks, worktree settings |
| `scripts/worktree-env.sh` | Per-worktree `.env` (compose project name, ports) |

## The loop for one task

One GitHub issue = one branch = one worktree = one agent session = one PR.

1. **Spec.** The task is a GitHub issue, small enough for one PR.
2. **Start.** `/implement-issue 12`, or asking a session to implement issue 12, moves the session into worktree `issue-12` on branch `worktree-issue-12`, created from `origin/main`. Every issue's worktree and branch get the same name, so nobody has to remember the convention. From a terminal, `claude -w issue-12` does the same.
3. **Build test-first** with the `tdd` skill: agree the seams to test, then red → green in vertical slices.
4. **Design review.** With tests green, `standards-review` checks the diff on two axes: does it follow the documented standards (plus a baseline of code smells), and does it match the issue? The agent refactors on its findings, with the tests as a safety net.
5. **Bug review.** The built-in `/code-review` runs on the final diff. It comes after the refactor so it reviews the code that ships.
6. **Rejection review.** The `rejection-review` skill gives every finding the author rejected to a fresh-context reviewer, which upholds or disputes the stated reason. A disputed rejection is fixed, or goes to the PR's Decide block if the author has an argument the reviewer didn't address. A rejection that rests on the issue's intent ("not needed yet", a non-goal) goes to Decide even when upheld, if the stakes are real: a reviewer can check the issue's wording, not what the person who wrote it meant. One with low stakes stays in the Review notes, marked "settled by reading the issue".
7. **Gate.** `make ci`, then push and open a PR. The `write-pr` skill fills in `.github/pull_request_template.md` for a reviewer who works like a coach: they hold the context and ask the questions, so the description points them where to look rather than arguing for the change. It opens with a plain explanation, so the reviewer forms a view before seeing the author's. Then come the decisions, each posed as a question in the reviewer's terms with neutral options, and the files to read closely. Mechanics, evidence and the **Review notes**, every review finding and how it was resolved, fold into collapsed sections.

The two reviews are kept separate on purpose: a design smell and a correctness bug call for different actions, and mixing them in one list buries the bugs.

### Who does what

Roles that need context and roles that need distance go to different agents:

- **Writing and fixing need context**, so the session that wrote the code also addresses the findings, as an author addresses review comments.
- **Reviewing needs distance.** Every review starts with a fresh context and sees the diff, the issue and the documented standards, never the author's reasoning: `standards-review` spawns its sub-agents that way, and `/code-review` runs as a forked skill, which doesn't see the conversation either.
- **Judging a rejection needs distance too.** A wrong fix is caught by tests and later reviews; a wrong rejection is caught by nothing when the author is the only judge. On the PR for issue #2, two of five rejections from the first review were overturned, and only because the bug review happened to raise them again. The rejection review makes that second look deliberate instead of lucky.

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

Package-wide checks such as `go vet` don't run on every edit: mid-refactor, half-finished code fails them on purpose, and feeding that back to the agent makes it patch code it is about to rewrite.

## Parallel agents with git worktrees

A worktree is a second working directory linked to the same repository, with its own branch checked out. Several agents can each work in their own worktree at the same time; their work meets only when PRs merge. Tasks that run in parallel should touch different packages, which is one reason the package layout matters.

Worktrees isolate files, not the machine. Ports, Docker containers and the NATS queue group are shared, so each worktree gets its own:

- **Compose project.** `COMPOSE_PROJECT_NAME=herald-<name>` gives each worktree its own Postgres, NATS and volumes. Sharing one NATS would let one worktree's dispatcher take another worktree's Deliveries.
- **Ports.** The main checkout is slot 0; each worktree takes the lowest free slot 1–9, and every published port is shifted by `slot × 100`.

`make up` generates `.env` on first use, so this works however the worktree was created, by a person or an agent. Integration tests use testcontainers and are isolated by construction.

Lifecycle:

```
/implement-issue <n>  →  make up  →  work, commit  →  make ci, push, PR  →  merge  →  exit the session  →  make worktree-rm NAME=issue-<n>
```

A session locks its worktree while it runs, so `make worktree-rm` refuses a worktree whose session is still open. Run it from the main checkout once that session has exited, or choose "remove" when the session asks on exit. A session that entered its worktree mid-conversation gets no such prompt, so `implement-issue` asks instead, and stops the worktree's stack before removing it.

## Carrying this over to a team

- Rules and skills are versioned with the code and reviewed in PRs like any other change, in PRs of their own so they are never buried in a feature diff. Agents propose setup changes; they don't make them in passing.
- Review notes in each PR description keep the evidence: findings that keep coming up become rules, and procedures repeated across PRs become skills. PRs are shared and permanent, so that's where the evidence lives.
- Hooks and Make targets work for every agent session and for people too, so the guarantees don't depend on everyone's personal setup.
- Personal working preferences (how a developer likes to be coached, which shortcuts they use) stay in their own `~/.claude/` and never land in the repo.
- Personal and project instructions are concatenated into the agent's context, not merged, so nothing decides between them when they conflict. `CLAUDE.md` therefore states that the project's instructions win, and asks the agent to report a conflict when it hits one. The repo can't see anyone's personal setup, so it has to carry this rule itself.
