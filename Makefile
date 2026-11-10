# Targets for building, testing and running Herald. `make ci` and the
# compose stack arrive with M1; see docs/ai-workflow.md for the worktree flow.

.PHONY: up down worktree-rm

# Per-checkout compose project name and ports. Generated on first use.
.env:
	./scripts/worktree-env.sh

up: .env
	docker compose up -d

down:
	docker compose down -v

# Stop a worktree's stack, then remove the worktree and its branch.
# Usage: make worktree-rm NAME=<name>   (the name given to `claude -w`)
worktree-rm:
	@test -n "$(NAME)" || { echo "usage: make worktree-rm NAME=<name>"; exit 1; }
	@wt=.claude/worktrees/$(NAME); \
	if [ -f "$$wt/.env" ]; then (cd "$$wt" && docker compose down -v); fi; \
	git worktree remove "$$wt" && git branch -D "worktree-$(NAME)"
