.PHONY: build vet fmt fmt-fix test test-race gosec govulncheck arch coverage ci

# GOBIN's tools (gopls, golangci-lint, gosec, govulncheck) live under
# $(go env GOPATH)/bin, which is not guaranteed to be on PATH for every
# invoking shell (AGENTS.md's own IDE & LSP Tooling section documents
# this exact gap). Prepending it here means `make ci` works the same way
# regardless of whether the invoking shell's profile was set up for it.
export PATH := $(shell go env GOPATH)/bin:$(PATH)

build:
	go build ./...

vet:
	go vet ./...

# gofmt -l as a hard failure: any output at all (a file gofmt would
# reformat) fails the target, per the Phase 0 CI harness item's explicit
# requirement. Excludes .claude/, which can hold other agents' git
# worktrees (full nested checkouts of this repo, each with its own
# untracked, possibly unformatted state) that are not part of this
# module's source tree.
fmt:
	@unformatted="$$(gofmt -l $$(find . -name '*.go' -not -path './.claude/*' -not -path './.git/*'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt would reformat:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Convenience target for actually fixing what `make fmt` catches, not
# part of the ci target (ci must fail on unformatted code, never fix it
# silently out from under the change under review).
fmt-fix:
	gofmt -w $$(find . -name '*.go' -not -path './.claude/*' -not -path './.git/*')

test:
	go test ./...

test-race:
	go test -race ./...

# gosec-check (tools/gosec-check) wraps gosec with the per-finding waiver
# file (gosec-waivers.json) the Phase 0 CI harness item's pre-existing-
# findings policy requires: every accepted finding is named individually,
# with a written reason, never suppressed by rule ID or directory as a
# whole. See gosec-waivers.json's own header comment.
gosec:
	go run ./tools/gosec-check

govulncheck:
	govulncheck ./...

# The Section 25 layering rules (PLAN.md's own Enforcement note, and the
# Phase 0 precondition that names it explicitly) as an ordinary go test,
# so it runs as part of `test`/`test-race` too; this target exists for
# running it on its own.
arch:
	go test ./internal/archtest/...

# coverage-check (tools/coverage-check) is the ratchet the Phase 0 CI
# harness item's coverage policy decision describes: no package may
# regress below coverage-floor.json's recorded floor. See that file's own
# header comment for the full policy and why a flat 90% gate is not used
# on day one.
coverage:
	go run ./tools/coverage-check

# ci is what a pull request must pass. -race, not plain test, is
# deliberately included here (not just in a separate target) because the
# Phase 0 item lists `go test -race ./...` as one thing CI must run, and
# splitting it out would make it easy to merge a PR that only ran the
# non-race target.
ci: build vet fmt test-race gosec govulncheck coverage
	@echo "ci: all checks passed"
