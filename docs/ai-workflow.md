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
| `.claude/skills/` | On-demand procedures: `tdd`, `codebase-design`, `standards-review` (adapted from [mattpocock/skills](https://github.com/mattpocock/skills), MIT) |
| `.claude/hooks/` | Scripts run by Claude Code on events |
| `.claude/settings.json` | Sandbox, hooks, worktree settings |
| `scripts/worktree-env.sh` | Per-worktree `.env` (compose project name, ports) |

## The loop for one task

One GitHub issue = one branch = one worktree = one agent session = one PR.

1. **Spec.** The task is a GitHub issue, small enough for one PR.
2. **Start.** `claude -w issue-12` creates an isolated worktree from `origin/main` and starts a session there.
3. **Build test-first** with the `tdd` skill: agree the seams to test, then red → green in vertical slices.
4. **Design review.** With tests green, `standards-review` checks the diff on two axes: does it follow the documented standards (plus a baseline of code smells), and does it match the issue? The agent refactors on its findings, with the tests as a safety net.
5. **Bug review.** The built-in `/code-review` runs on the final diff. It comes after the refactor so it reviews the code that ships.
6. **Gate.** `make ci`, then push and open a PR with `Closes #12`.

The two reviews are kept separate on purpose: a design smell and a correctness bug call for different actions, and mixing them in one list buries the bugs.

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
claude -w <name>  →  make up  →  work, commit  →  make ci, push, PR  →  merge  →  make worktree-rm NAME=<name>
```

## Carrying this over to a team

- Rules and skills are versioned with the code and reviewed in PRs like any other change. A good source of new rules is review comments that keep coming up.
- Hooks and Make targets work for every agent session and for people too, so the guarantees don't depend on everyone's personal setup.
- Personal working preferences (how a developer likes to be coached, which shortcuts they use) stay in their own `~/.claude/` and never land in the repo.
