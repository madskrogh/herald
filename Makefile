# Targets for building, testing and running Herald. See docs/ai-workflow.md
# for the worktree flow.

.PHONY: ci lint test breaking generate check-generated up down admin worktree-rm

# Everything CI runs on a PR. Run it before every push.
ci: lint test breaking check-generated

lint:
	golangci-lint run ./...
	buf lint

test:
	go test -race ./...

# The ref breaking compares the protos with. Override it to compare with
# another commit: make breaking AGAINST=<ref>
AGAINST ?= origin/main

# Fails on a change to the protos that breaks compatibility with the merge-base
# of HEAD and AGAINST: what this branch changed, not what AGAINST changed since
# the branch started. Each module in buf.yaml is compared on its own, under its
# own rule set. A module the merge-base doesn't have is new, with nothing to
# break yet, so it passes with a message. A ref that doesn't exist fails, so a
# missing fetch can't pass as "nothing to compare", and so does an empty
# module list. The commit is resolved here, so that buf, which reads the
# shared git directory, compares with the same commit whichever worktree runs
# this.
breaking:
	@commit=$$(git merge-base HEAD '$(AGAINST)' 2>/dev/null) || { \
		echo "breaking: no merge-base of HEAD and $(AGAINST)"; exit 1; }; \
	modules=$$(buf config ls-modules) && test -n "$$modules" || { \
		echo "breaking: no modules in buf.yaml"; exit 1; }; \
	gitdir=$$(git rev-parse --git-common-dir); \
	for module in $$modules; do \
		if git cat-file -e "$$commit:$$module" 2>/dev/null; then \
			buf breaking "$$module" --against "$$gitdir#ref=$$commit,subdir=$$module" || exit 1; \
		else \
			echo "breaking: $(AGAINST) has no $$module module, so there is nothing to compare it with"; \
		fi; \
	done

# Regenerates all committed generated code.
generate:
	sqlc generate
	buf generate
	buf generate --template buf.gen.openapi.yaml

# Directories holding committed generated code, checked by check-generated.
GENERATED_DIRS := internal/ingest/ingestdb internal/gen docs/openapi

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

# --build so the stack always runs this checkout's code and migrations.
up: .env
	docker compose up -d --wait --build

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
