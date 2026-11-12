---
name: implement-issue
description: Start work on a GitHub issue in its own worktree, named after the issue. Use when the user asks to implement, build, fix or work on a GitHub issue by number ("implement issue 32", "work on #32"), or runs /implement-issue <n>. Also use when leaving an issue's worktree.
argument-hint: <issue number>
---

One GitHub issue = one worktree = one branch = one PR. This skill makes the first step the same every time, so worktrees and branches are always named after their issue: worktree `issue-<n>`, branch `worktree-issue-<n>`. A later session can then find the issue's worktree and resume it instead of starting a second one.

Issue: $ARGUMENTS

## 1. Get the issue

Take the issue number from the arguments or the request; if there is none, ask for it. Run `gh issue view <n>`. If the issue doesn't exist or is closed, stop and tell the user.

## 2. Enter its worktree

- **Not in a worktree:** if `.claude/worktrees/issue-<n>/` exists, the issue was started before: enter it with `EnterWorktree` and `path`. Otherwise create it with `EnterWorktree` and `name: issue-<n>`. That branches `worktree-issue-<n>` from `origin/main`.
- **Already in worktree `issue-<n>`:** carry on.
- **In any other worktree:** stop and ask. The work belongs in its own worktree.

## 3. Build it

Follow the Workflow in `CLAUDE.md` from step 2: `tdd`, then the reviews, `make ci` and the PR.

## 4. Leave the worktree

When the user asks to exit or leave, or says the PR is merged, never pick keep or remove yourself. Find out the state (`gh pr view --json state`, `git status --short`), then ask with `AskUserQuestion`, stating whether the PR is merged and whether there is uncommitted work:

- **Keep:** `ExitWorktree` with `action: keep`. The worktree, its branch and its stack stay for later.
- **Remove:** if `.env` exists, run `make down` first: `ExitWorktree` doesn't stop the worktree's compose stack, and its containers and volumes would outlive it. Then `ExitWorktree` with `action: remove`. Squash merges leave the branch's commits looking unmerged, so the tool refuses; pass `discard_changes: true` only when the PR is merged and nothing is uncommitted. Otherwise list what would be lost and confirm first.
