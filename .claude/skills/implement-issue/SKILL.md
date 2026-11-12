---
name: implement-issue
description: Start work on a GitHub issue in its own worktree, named after the issue. Use when the user asks to implement, build, fix or work on a GitHub issue by number ("implement issue 32", "work on #32"), or runs /implement-issue <n>.
argument-hint: <issue number>
---

One GitHub issue = one worktree = one branch = one PR. This skill makes the first step the same every time, so worktrees and branches are always named after their issue: worktree `issue-<n>`, branch `worktree-issue-<n>`. `make worktree-rm NAME=issue-<n>` relies on that name.

Issue: $ARGUMENTS

## 1. Get the issue

Take the issue number from the arguments or the request; if there is none, ask for it. Run `gh issue view <n>`. If the issue doesn't exist or is closed, stop and tell the user.

## 2. Enter its worktree

- **Not in a worktree:** if `.claude/worktrees/issue-<n>/` exists, the issue was started before: enter it with `EnterWorktree` and `path`. Otherwise create it with `EnterWorktree` and `name: issue-<n>`. That branches `worktree-issue-<n>` from `origin/main`.
- **Already in worktree `issue-<n>`:** carry on.
- **In any other worktree:** stop and ask. The work belongs in its own worktree.

## 3. Build it

Follow the Workflow in `CLAUDE.md` from step 2: `tdd`, then the reviews, `make ci` and the PR.
