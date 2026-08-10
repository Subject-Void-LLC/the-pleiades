.PHONY: build vet fmt fmt-fix test test-race test-integration gosec govulncheck arch coverage docs-lint docs-gen-check tools hooks ci

# GOBIN's tools (gopls, golangci-lint, gosec, govulncheck) live under
# $(go env GOPATH)/bin, which is not guaranteed to be on PATH for every
# invoking shell (AGENTS.md's own IDE & LSP Tooling section documents
# this exact gap). Prepending it here means `make ci` works the same way
# regardless of whether the invoking shell's profile was set up for it.
export PATH := $(shell go env GOPATH)/bin:$(PATH)

# Pinned versions of the two external scanners `make ci` shells out to,
# and the single place either version is written down: the CI workflow
# installs them by calling `make tools` rather than naming a version of
# its own, so there is no second copy to drift.
#
# They are pinned, rather than tracking @latest as the workflow used to,
# because @latest made CI non-reproducible in both directions. A new
# gosec release could turn CI red on a commit that changed nothing about
# this repository, and a developer whose own binary was older would pass
# locally on exactly the code CI rejected -- which is not hypothetical:
# the pin below was introduced while local gosec was v2.26.1 and CI was
# resolving @latest to v2.28.0, two minor versions of rule changes apart.
# Bumping either version is now a deliberate commit whose diff shows
# which findings the bump introduced.
#
# Note this pins the govulncheck *scanner*, not the vulnerability
# database: that is fetched from https://vuln.go.dev at run time, by
# design, so a newly published advisory against a dependency still fails
# CI the day it lands. That is the intended behavior, and it is not a
# local/CI divergence, since both sides query the same database.
GOSEC_VERSION       ?= v2.28.0
GOVULNCHECK_VERSION ?= v1.6.0

# ensure-tool installs $(2)@$(3) as command $(1), but only when what is
# already on PATH is not already exactly that version, which keeps a
# repeat `make ci` fast and lets it run with no network once the tools
# are in place.
#
# The version comes from `go version -m`, which reads the module version
# the Go toolchain embeds into every binary it builds, rather than from
# the tool's own --version flag: gosec stamps its own version string with
# release-time ldflags that `go install` does not apply, so a
# `go install`ed gosec reports "dev" regardless of which tag it was
# actually built from, and would reinstall itself on every single run.
define ensure-tool
	have="$$(go version -m "$$(command -v $(1) 2>/dev/null)" 2>/dev/null | awk '$$1=="mod"{print $$3; exit}')"; \
	if [ "$$have" != "$(3)" ]; then \
		echo "tools: installing $(1) $(3) (found: $${have:-none})"; \
		go install $(2)@$(3); \
	fi
endef

# tools brings this machine's scanners to the pinned versions above. It
# is a prerequisite of the two targets that use them, so a local `make
# ci` cannot silently check with a different scanner than CI does.
tools:
	@$(call ensure-tool,gosec,github.com/securego/gosec/v2/cmd/gosec,$(GOSEC_VERSION))
	@$(call ensure-tool,govulncheck,golang.org/x/vuln/cmd/govulncheck,$(GOVULNCHECK_VERSION))

# hooks points this clone's Git hooks at the tracked .githooks directory,
# so `git push` runs the same `make ci` the CI job runs and a failure
# lands here instead of on a pushed branch. This is deliberately opt-in
# per clone rather than automatic: Git never executes a hook that arrived
# with a fetch until the person who cloned the repository asks it to, and
# core.hooksPath is local config, not a tracked file. Run it once per
# clone; see .githooks/pre-push for what it does and how to skip it.
hooks:
	git config core.hooksPath .githooks
	@echo "hooks: 'git push' will now run .githooks/pre-push (make ci) first; skip a single push with --no-verify"

build:
	go build ./...

# Two passes, because go vet respects build tags: without the second one
# the integration-tagged files (the largest tests in this repository)
# would never be vetted at all.
vet:
	go vet ./...
	go vet -tags integration ./...

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

# GO_TEST_TIMEOUT replaces `go test`'s 10-minute per-package default,
# which is not enough headroom once container startup timeouts are
# explicit (internal/testsupport's ContainerStartupTimeout /
# SSHDStartupTimeout). The two are coupled: internal/lock starts seven
# containers in a single package, so a package where every start timed
# out needs 7 x 2 minutes to surface the real "container failed to start"
# error. If the package timeout fired first it would replace that clear
# message with a goroutine-dump panic saying nothing about Docker, which
# is the failure mode this value exists to prevent, not merely a slower
# one. 20m covers that worst case with room for the tests themselves,
# while still bounding a genuinely hung run; the slowest package today is
# internal/event at roughly two minutes.
#
# tools/coverage-check runs its own `go test ./...` and passes the same
# value from a constant of its own, rather than reading this one out of
# the environment, which would make an exec.Command argument
# caller-controlled for no benefit. Its TestGoTestTimeoutMatchesMakefile
# asserts the two agree, so changing this line alone fails the build
# rather than silently leaving the two runs bounded differently.
GO_TEST_TIMEOUT ?= 20m

test:
	go test -timeout $(GO_TEST_TIMEOUT) ./...

test-race:
	go test -race -timeout $(GO_TEST_TIMEOUT) ./...

# test-integration runs everything behind the `integration` build tag:
# the Grand Integration Test (the real controller and runner binaries
# against real Postgres and NATS containers) and internal/ent's
# migration-parity check. AGENTS.md requires integration tests to carry
# the tag and run separately, and nothing else in this Makefile passes
# -tags, so without this target they would never run at all.
#
# -race because that is the build `ci` judges everywhere else, and
# because tests/e2e's own raceTimeScale constants are keyed on it.
#
# -count=1 is not belt and braces. tests/e2e reaches cmd/controller and
# cmd/runner by building them as subprocesses rather than importing them,
# so Go's test cache sees no dependency on either binary's source and will
# replay a stale PASS after a controller change. See tests/e2e's own
# harness doc comment.
test-integration:
	go test -tags integration -race -count=1 -timeout $(GO_TEST_TIMEOUT) ./...

# gosec-check (tools/gosec-check) wraps gosec with the per-finding waiver
# file (gosec-waivers.json) the Phase 0 CI harness item's pre-existing-
# findings policy requires: every accepted finding is named individually,
# with a written reason, never suppressed by rule ID or directory as a
# whole. See gosec-waivers.json's own header comment.
gosec: tools
	go run ./tools/gosec-check

govulncheck: tools
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

# docs-lint (tools/docs-lint) fails the build when a gitignored internal
# document (.SPECIFICATION/, .AGENTS/, PLAN.md, PATTERNS.md,
# IMPLEMENTATION.md) is cited anywhere a real user could see it: docs/,
# the CLI's own --help text, and the packages the documentation
# generation pipeline folds into generated reference pages. No waiver
# file, unlike gosec-check: there is no legitimate reason for a citation
# into a file the shipped binary does not contain.
docs-lint:
	go run ./tools/docs-lint

# docs-gen-check proves the committed docs/reference/ and
# internal/api/wellknown/ trees are exactly what tools/gendocs produces
# from the current source, the same "regenerate and diff" discipline the
# plan's Part 4 Step 6 asks for. It runs the one generator directly rather
# than through `go generate`, for two reasons. First, `go generate ./...`
# would also fire internal/forge/catalogdata/doc.go's directive for
# tools/gencatalog, which shells out to `forge new-collection` and refuses
# to overwrite files that already exist (see LESSONS_LEARNED.md #69), so
# running it over an already-generated tree fails the build instead of
# proving anything. Second, `go generate` runs a directive in its own
# package's directory, and gendocs writes to repo-root-relative paths:
# invoking it that way wrote a full copy of docs/reference and
# internal/api/wellknown under tools/gendocs/ and left the two trees the
# git diff below inspects untouched. This target passed for as long as it
# regenerated nothing.
docs-gen-check:
	go run ./tools/gendocs
	git diff --exit-code -- docs/reference internal/api/wellknown
	test -z "$$(git ls-files --others --exclude-standard -- docs/reference internal/api/wellknown)"

# ci is what a pull request must pass. -race, not plain test, is
# deliberately included here (not just in a separate target) because the
# Phase 0 item lists `go test -race ./...` as one thing CI must run, and
# splitting it out would make it easy to merge a PR that only ran the
# non-race target.
ci: build vet fmt test-race test-integration gosec govulncheck coverage docs-lint docs-gen-check
	@echo "ci: all checks passed"
