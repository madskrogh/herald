# Targets for building, testing and running Herald. See docs/ai-workflow.md
# for the worktree flow.

.PHONY: ci lint test breaking generate check-generated wait-ci up down e2e endpoint profile admin worktree-rm

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
GENERATED_DIRS := internal/ingest/ingestdb internal/delivery/deliverydb internal/gen docs/openapi

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

# How long wait-ci waits for a CI run to appear, in seconds.
WAIT_CI_TIMEOUT ?= 120

# Waits for the CI workflow's run on HEAD's commit to finish and exits with its result.
# Run it after pushing to a branch with an open PR. GitHub starts no run for a
# PR that conflicts with its base. Right after a push the run
# may not exist yet, and `gh pr checks --watch` exits with "no checks reported"
# instead of waiting, so this first waits for the run to appear. It matches the
# run by commit, not branch, so an earlier push's finished run can't answer.
# The watch's progress is discarded, and only the final summary is printed.
wait-ci:
	@sha=$$(git rev-parse HEAD); \
	deadline=$$(( $$(date +%s) + $(WAIT_CI_TIMEOUT) )); \
	while :; do \
		id=$$(gh run list --workflow ci.yml --commit "$$sha" --limit 1 --json databaseId --jq '.[].databaseId') || exit 1; \
		test -n "$$id" && break; \
		if [ "$$(date +%s)" -ge "$$deadline" ]; then \
			echo "wait-ci: no CI run for $$sha after $(WAIT_CI_TIMEOUT)s. Is the commit pushed, with a PR open and no merge conflicts?"; \
			exit 1; \
		fi; \
		sleep 5; \
	done; \
	gh run watch "$$id" --interval 15 >/dev/null || { \
		echo "wait-ci: stopped watching run $$id before it finished: check it with 'gh run view $$id'"; \
		exit 1; }; \
	gh run view "$$id" --exit-status

# Per-checkout compose project name and ports. Generated on first use, and
# regenerated, in the same slot, when the script changes, so a checkout's
# .env gains the ports of services added since. Its message goes to stderr,
# so that a target's output, such as make admin's, can still be captured.
.env: scripts/worktree-env.sh
	@./scripts/worktree-env.sh >&2

# --build so the stack always runs this checkout's code and migrations.
up: .env
	docker compose up -d --wait --build

# The host address that Endpoints served from the host listen on, for the
# end-to-end tests and make endpoint: the one host.docker.internal reaches
# from a container. Docker Desktop reaches the host's loopback; on Linux it is
# the default bridge's gateway, which compose.yaml maps the name to.
ifeq ($(shell uname -s),Linux)
HOST_ENDPOINT_IP = $(shell docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
else
HOST_ENDPOINT_IP = 127.0.0.1
endif

# Runs the end-to-end tests against this checkout's stack, started with
# `make up`. Without these variables the tests skip, as in `make test`.
# RUN selects tests as go test's -run does; without it, all of them run.
# Usage: make e2e RUN=TestSpanIsStoredInClickHouse
e2e: .env
	@. ./.env && \
		HERALD_E2E_API_URL="http://localhost:$$HERALD_API_HTTP_PORT" \
		HERALD_E2E_DATABASE_URL="postgres://herald:herald@localhost:$$HERALD_POSTGRES_PORT/herald?sslmode=disable" \
		HERALD_E2E_ENDPOINT_IP="$(HOST_ENDPOINT_IP)" \
		HERALD_E2E_OTLP_URL="http://localhost:$$HERALD_OTLP_HTTP_PORT" \
		HERALD_E2E_CLICKHOUSE_URL="http://herald:herald@localhost:$$HERALD_CLICKHOUSE_HTTP_PORT" \
		go test -race -count=1 $(if $(RUN),-run '$(RUN)') ./internal/e2e/

# Serves an Endpoint for this checkout's stack that prints each Attempt it
# receives. Given the Endpoint's signing secret, it also says whether each
# signature verifies. The README walks through it.
# Usage: make endpoint [SECRET=whsec_...]
endpoint: .env
	@test -n '$(HOST_ENDPOINT_IP)' || { echo "endpoint: no host address to listen on: is Docker running?"; exit 1; }
	@. ./.env && go run ./cmd/demo-endpoint \
		-listen "$(HOST_ENDPOINT_IP):$$HERALD_DEMO_ENDPOINT_PORT" \
		-url "http://host.docker.internal:$$HERALD_DEMO_ENDPOINT_PORT/" \
		-secret '$(SECRET)'

# The checkout whose stack is the base build for profile: by default the main
# checkout, the first entry of `git worktree list`.
BASE ?= $(shell git worktree list --porcelain | sed -n '1s/^worktree //p')

# Profiles this checkout's stack (the candidate) and BASE's at the same time,
# and prints how the candidate's profiles differ from the base's. Both stacks
# must be up, and get the same traffic while it runs. scripts/profile.sh says
# what it captures; DURATION and OUT pass through to it. Each build is labelled
# with its checkout's commit, which is the stack's only if the stack was
# started (make up) at that commit.
# Usage: make profile [BASE=<checkout>] [DURATION=<seconds>]
profile: .env
	@test -d '$(BASE)' || { echo "profile: the base checkout $(BASE) doesn't exist"; exit 1; }
	@test "$$(cd '$(BASE)' && pwd -P)" != "$$(pwd -P)" || { \
		echo "profile: the base is this checkout; set BASE to another checkout"; exit 1; }
	@label() { \
		echo "$$1 at $$(git -C "$$1" rev-parse --short HEAD 2>/dev/null || echo 'an unknown commit')$$( \
			test -z "$$(git -C "$$1" status --porcelain 2>/dev/null)" || echo ', with uncommitted changes')"; }; \
	BASE_LABEL=$$(label '$(BASE)') CANDIDATE_LABEL=$$(label '$(CURDIR)'); \
	port() { \
		p=$$(sed -n "s/^$$2=//p" "$$1/.env" 2>/dev/null); \
		test -n "$$p" || { echo "profile: $$1/.env has no $$2: run 'make up' there" >&2; exit 1; }; \
		echo "localhost:$$p"; }; \
	CANDIDATE_API=$$(port . HERALD_API_PPROF_PORT) && \
	CANDIDATE_DISPATCHER=$$(port . HERALD_DISPATCHER_PPROF_PORT) && \
	BASE_API=$$(port '$(BASE)' HERALD_API_PPROF_PORT) && \
	BASE_DISPATCHER=$$(port '$(BASE)' HERALD_DISPATCHER_PPROF_PORT) && \
	export CANDIDATE_API CANDIDATE_DISPATCHER BASE_API BASE_DISPATCHER BASE_LABEL CANDIDATE_LABEL && \
		./scripts/profile.sh

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
