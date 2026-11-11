# Herald

Herald delivers webhooks on behalf of SaaS products: signed, retried, and observable. It is a portfolio project built to production standards, and a working lab for agent-assisted development. How agents are used here is described in [docs/ai-workflow.md](docs/ai-workflow.md).

## Read before changing code

- [docs/design.md](docs/design.md): product spec, architecture, delivery semantics, milestones M1–M5.
- [docs/adr/](docs/adr/): settled decisions. Don't reopen them in passing; propose a new ADR if one needs to change.
- [GLOSSARY.md](GLOSSARY.md): domain language. Use these terms in code, tests, comments and docs, and avoid the listed alternatives.

## Commands

| Command | What it does |
|---|---|
| `make up` | Start the local stack with `docker compose` (generates `.env` on first run) |
| `make down` | Stop the stack and delete its volumes |
| `make ci` | Everything CI runs on a PR: lint, tests with `-race`, buf checks, generated-code check |
| `make worktree-rm NAME=x` | Stop a worktree's stack, remove the worktree and its branch |

Run `make ci` before every push. It is exactly what CI runs.

## Conventions

- Go conventions are in `.claude/rules/`, loaded automatically by path: `go.md`, `go-testing.md`, `sql.md`.
- Generated code (sqlc, buf) is committed. Never edit it by hand; change the source and regenerate.
- Commit messages: short and imperative (`Add outbox relay`).
- Issues, PR descriptions, design docs, ADRs and code comments describe Herald, not the tools used to build it: no agent or tool specifics, and nothing about one machine's setup (local paths, defaults, sandbox behaviour). The agent-setup files (`CLAUDE.md`, `.claude/`, `docs/ai-workflow.md`) may name the tools, but still nothing about one machine's setup.

## Workflow

One GitHub issue = one branch = one worktree = one PR.

1. Read the issue: `gh issue view <n>`.
2. Build it test-first with the `tdd` skill. Agree the seams before writing tests.
3. Tests green: run the `standards-review` skill against `main`, and refactor on its findings.
4. Run the built-in `/code-review` on the final diff for bugs.
5. `make ci`, push, and open a PR whose description says `Closes #<n>` and has a **Review notes** section: each finding from steps 3 and 4, and whether it was fixed or rejected (with the reason). These notes are the evidence for later changes to the agent setup.

Agent setup (`CLAUDE.md`, `.claude/rules/`, `.claude/skills/`, `.claude/hooks/`, `.claude/settings.json`) changes only in its own PR, never inside a feature PR. When a task suggests a new rule or skill, propose it under Review notes instead of making the change.

Worktrees: `claude -w <name>` creates `.claude/worktrees/<name>/` on branch `worktree-<name>` from `origin/main`. Each worktree gets its own compose project and ports through `.env` (see `scripts/worktree-env.sh`). Integration tests use testcontainers and need nothing from `.env`.
