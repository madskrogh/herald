# Targets for building, testing and running Herald. See docs/ai-workflow.md
# for the worktree flow.

.PHONY: ci lint test generate check-generated up down admin worktree-rm

# Everything CI runs on a PR. Run it before every push.
ci: lint test check-generated

lint:
	golangci-lint run ./...

test:
	go test -race ./...

# Regenerates all committed generated code. buf adds its step here.
generate:
	sqlc generate

# Directories holding committed generated code, checked by check-generated.
GENERATED_DIRS := internal/ingest/ingestdb

# Fails if go.mod/go.sum are untidy, or if regenerating changes any generated
# code relative to the index (including new files it would add). Staged
# generated code counts as up to date, so this passes before a commit too.
check-generated: generate
	go mod tidy -diff
ifneq ($(strip $(GENERATED_DIRS)),)
	@git diff --exit-code --stat -- $(GENERATED_DIRS) && \
	test -z "$$(git ls-files --others --exclude-standard -- $(GENERATED_DIRS))" || { \
		git status --short -- $(GENERATED_DIRS); \
		echo "generated code is out of date: run 'make generate' and stage the result"; \
		exit 1; }
endif

# Per-checkout compose project name and ports. Generated on first use.
.env:
	./scripts/worktree-env.sh

up: .env
	docker compose up -d --wait

# Runs herald-admin against this checkout's stack.
# Usage: make admin ARGS='tenant create --name Acme'
admin: .env
	@. ./.env && HERALD_DATABASE_URL="postgres://herald:herald@localhost:$$HERALD_POSTGRES_PORT/herald?sslmode=disable" \
		go run ./cmd/herald-admin $(ARGS)

down:
	docker compose down -v

# Stop a worktree's stack, then remove the worktree and its branch.
# Usage: make worktree-rm NAME=<name>   (the name given to `claude -w`)
# A session working in the worktree locks it; this refuses to touch a locked
# worktree, before stopping its stack.
worktree-rm:
	@test -n "$(NAME)" || { echo "usage: make worktree-rm NAME=<name>"; exit 1; }
	@wt=.claude/worktrees/$(NAME); \
	lock=$$(git worktree list --porcelain | awk -v wt="$(CURDIR)/$$wt" \
		'$$1 == "worktree" { cur = $$2 } $$1 == "locked" && cur == wt { sub(/^locked ?/, ""); print; exit }'); \
	if [ -n "$$lock" ]; then \
		echo "worktree-rm: $$wt is locked ($$lock)."; \
		echo "Exit the session using it, then run this again."; \
		exit 1; \
	fi; \
	if [ -f "$$wt/.env" ]; then (cd "$$wt" && docker compose down -v); fi; \
	git worktree remove "$$wt" && git branch -D "worktree-$(NAME)"
