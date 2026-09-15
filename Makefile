.PHONY: build devtools vet fmt fmt-fix tidy-check test test-race test-no-docker test-repeat test-integration gosec govulncheck arch coverage docs-lint docs-gen-check helm-lint templ-gen templ-gen-check tools hooks lsp commitgate sweep sweep-dry sweep-timer sweep-timer-off dev-cert ui-dev ui-stop break-glass image-tools image-scan ci ci-remote push-gate push-gate-race push-gate-integration push-gate-coverage

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
# The module path, read from go.mod rather than written down a second
# time, so `make lsp` below cannot check for a module name this repository
# no longer has.
MODULE_PATH := $(shell awk '/^module /{print $$2}' go.mod)

# How long `make lsp` holds the MCP server's stdin open waiting for a
# reply. gopls has to load and type-check the whole workspace before it
# can answer go_workspace, which is cold-cache work on a first run; the
# handshake closes stdin the moment the module path shows up in the
# reply, so this is a ceiling on a broken setup rather than a cost a
# working one pays.
LSP_HANDSHAKE_SECONDS ?= 45

GOSEC_VERSION       ?= v2.28.0
GOVULNCHECK_VERSION ?= v1.6.0

# The container image scanner, pinned the same way and installed by a
# SEPARATE target: see image-scan below for why it is not part of ci.
# Unlike the two above, trivy's own --version reports "dev" when built by
# `go install`, so ensure-tool's `go version -m` reading is what makes this
# pin checkable at all.
TRIVY_VERSION       ?= v0.58.2

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

# image-tools is separate from tools above because image-scan is separate
# from ci: nothing in ci builds an image, so nothing in ci has an image to
# scan, and making every `make ci` install a scanner it cannot use would be
# a cost with no return.
image-tools:
	@$(call ensure-tool,trivy,github.com/aquasecurity/trivy/cmd/trivy,$(TRIVY_VERSION))

# lsp proves this machine's Go language server is usable by an agent
# rather than merely installed, which are different claims. It is the
# check behind this file's LSP over grep mandate: an agent that cannot
# reach gopls falls back to grep, and a grep derived claim about Go
# semantics is a guess.
#
# What it checks, in the order a failure would bite:
#
#   1. gopls resolves on PATH. The export at the top of this file puts
#      $(go env GOPATH)/bin there for make, but Claude Code spawns an MCP
#      server from its own shell, so PATH has to be set persistently for
#      the agent too (see this file's IDE & LSP Tooling section).
#   2. gopls speaks MCP. `gopls mcp` is the headless server .mcp.json
#      wires in, and it is what turns one grep over the tree into one
#      typed query.
#   3. gopls loads THIS module. The handshake ends with a real
#      go_workspace call and greps the answer for the module path, which
#      is the only one of the three a version string cannot fake: a gopls
#      too old for go.mod's toolchain prints its version happily and then
#      type-checks nothing.
#
# Deliberately not part of ci: CI never invokes gopls, and gopls is the
# one tool here that is not pinned for exactly that reason (see tools).
lsp:
	@command -v gopls >/dev/null 2>&1 || { echo "lsp: gopls is not on PATH. Install it with: go install golang.org/x/tools/gopls@latest"; exit 1; }
	@gopls version | head -1
	@out="$$(mktemp)"; \
	trap 'rm -f "$$out"' EXIT INT TERM; \
	{ printf '%s\n' \
		'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"make-lsp","version":"0"}}}' \
		'{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
		'{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"go_workspace","arguments":{}}}'; \
		i=0; \
		while [ "$$i" -lt "$(LSP_HANDSHAKE_SECONDS)" ] && ! grep -qF '$(MODULE_PATH)' "$$out" 2>/dev/null; do \
			sleep 1; i=$$((i+1)); \
		done; } \
		| gopls mcp >"$$out" 2>/dev/null; \
	grep -qF '$(MODULE_PATH)' "$$out" \
		|| { echo "lsp: gopls answered no go_workspace for $(MODULE_PATH) within $(LSP_HANDSHAKE_SECONDS)s. The server is installed but not working on this tree: run 'gopls check ./cmd/pleiades/main.go' for the real error, and confirm gopls is new enough for go.mod's Go version."; exit 1; }
	@echo "lsp: gopls mcp answers go_workspace for $(MODULE_PATH); the agent's LSP tooling is live"

# hooks points this clone's Git hooks at the tracked .githooks directory,
# enabling all three of them at once:
#
#   pre-commit   tools/commitgate over the staged content (well under a
#                second: no build, no tests, index only)
#   commit-msg   tools/commitgate over the commit message
#   pre-push     push-gate, everything `make ci` runs with
#                test-race/test-integration swapped for tools/testgate's
#                more tolerant equivalents (see push-gate's own comment)
#
# so a rule AGENTS.md states lands at the moment it is broken rather than
# three commits later, and a failing gate lands here instead of on a
# pushed branch. This is deliberately opt-in per clone rather than
# automatic: Git never executes a hook that arrived with a fetch until the
# person who cloned the repository asks it to, and core.hooksPath is local
# config, not a tracked file. Run it once per clone; see each hook for
# what it does and how to skip it.
hooks:
	git config core.hooksPath .githooks
	@echo "hooks: 'git commit' now runs .githooks/pre-commit and .githooks/commit-msg (make commitgate), and 'git push' runs .githooks/pre-push (make push-gate); skip a single one with --no-verify"

# commitgate runs the commit-time gate by hand, against whatever is
# staged right now. The pre-commit hook runs exactly this, so it is the
# way to see what a commit would be told before making one.
commitgate:
	go run ./tools/commitgate

# sweep removes stale build output this working tree no longer needs: the
# static binaries every cmd/ composition root and tools/gendocs link, plus
# leftover .log/.test/.out files. Roughly 300 MB accumulates here after a
# full `make ci`, and `make build` regenerates all of it.
#
# Nothing is removed unless `git check-ignore` confirms it is ignored, and
# directories are skipped outright, so the gitignored internal document
# trees (.AGENTS/, .DESIGN/, .IGNORE/, .SPECIFICATION/) cannot be caught by
# it. Default age threshold is 7 days, so today's binary is left alone.
sweep:
	go run ./tools/sweep

# sweep-dry reports what sweep would remove and removes nothing. Run this
# first if you have never run the sweep on this checkout.
sweep-dry:
	go run ./tools/sweep -dry-run

# sweep-timer installs a systemd user timer that runs the sweep weekly.
# This WSL distro boots systemd (/etc/wsl.conf sets systemd=true), so a
# user timer is the mechanism that actually fires here. `loginctl
# enable-linger` is what lets it run when no shell is open.
sweep-timer:
	go run ./tools/sweep -install-timer

# sweep-timer-off stops and removes the weekly timer.
sweep-timer-off:
	go run ./tools/sweep -remove-timer

build:
	go build ./...

# devtools compiles the repo-local developer commands (tools/devcert,
# tools/uidev), which the default build cannot see.
#
# They carry `//go:build devtools` so they stay out of `go build ./...`,
# out of the shipped binaries and out of gosec's judgement of shipped
# server code. The tag used to be `ignore`, and the difference is not
# cosmetic: nothing at all compiled them, so a break in `make dev-cert` or
# `make ui-dev` stayed invisible until a human ran it and hit a compile
# error in a file no gate had ever looked at. Building AND vetting them
# here costs a second and closes that. How they are invoked does not
# change: `go run` on an explicitly named file ignores build constraints.
# Three passes, and the third is here for the same reason vet below runs
# twice: `go test` respects build tags, so a _test.go file behind the
# devtools tag is never compiled, never run and never reported by any other
# target in this file. tools/breakglass is the first devtools package with
# tests, and without this line its tests would have been written, would have
# passed locally, and would have been invisible to the gate that decides
# whether a change lands. Seven seconds, most of it tools/gencatalog, which
# test-race pays for separately and unavoidably.
devtools:
	go build -tags devtools ./tools/...
	go vet -tags devtools ./tools/...
	go test -tags devtools ./tools/...

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
# internal/event at roughly six minutes, most of which is one deliberate
# sleep: Phase 96a's release gate severs a real broker for 150 seconds,
# because the defect it guards (a connection that gave up for good at
# 2m3s) cannot be reproduced by any shorter outage. The 5-second severance
# in the older chaos test in the same package is why that test passed for
# the whole time the bug existed.
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

# DOCKER_TEST_PARALLELISM is how many container-provisioning packages
# `go test` may run at once. It is the fix for the condition
# FAILURE_PATTERNS.md #61 describes and flaky-packages.json tolerates:
# the failing package changes between runs, and every one of them
# provisions real containers.
#
# `go test` defaults -p to GOMAXPROCS, which is 20 on this project's own
# development host, and DOCKER_DEPENDENT_PACKAGES below names 22
# packages. So the default asks one Docker daemon to build, start, port
# map and health check the containers of twenty packages simultaneously,
# on top of a Ryuk reaper per package. Two consecutive full `make ci`
# runs on 2026-09-13 failed that way in seven different packages between
# them, and the failures were the daemon's own, not any test's: a
# `containers/<id>/json` inspect call exceeding its deadline after 553
# retries, a published port answering with connection refused, a NATS
# container never reachable on its mapped port.
#
# One, not a tuned number. The serial case is the only value justified
# without measuring this specific machine, and a number chosen to be
# just fast enough on the host that chose it is a number that saturates
# a smaller one. It costs wall clock and buys a gate that can pass:
# these packages now take the sum of their runtimes rather than the max,
# which is minutes, against a gate that could not go green at all.
# Raising it is a real decision to make with measurements, which is why
# it is an overridable variable rather than a literal in three recipes.
DOCKER_TEST_PARALLELISM ?= 1

# test-race is the -race pass over everything, split in two so the
# packages that provision containers do not run on top of each other.
# Both halves always run and the worst exit status wins, so one group's
# failure never hides the other's: a fail-fast && would report the fast
# packages and say nothing about the slow ones, which are the ones this
# split exists for.
#
# Both lists are built from one `go list` of the pattern this target
# would otherwise have passed straight to `go test`, and the container
# half is its INTERSECTION with DOCKER_DEPENDENT_PACKAGES rather than
# that list itself. Naming a package explicitly is not the same as
# matching it with ./...: `go test ./...` silently passes over a package
# whose build constraints exclude every file, while naming it is a hard
# "build constraints exclude all Go files ... [setup failed]". Two of
# the packages in the list are exactly that under these tags,
# tests/e2e (integration) and internal/ent/migrate/gen, so the
# intersection is what keeps this split from changing which packages the
# target covers.
test-race:
	@all="$$(go list ./...)"; \
	rest="$$all"; \
	docker=""; \
	for pkg in $(DOCKER_DEPENDENT_PACKAGES); do \
		if echo "$$all" | grep -q "^$$pkg$$"; then docker="$$docker $$pkg"; fi; \
		rest="$$(echo "$$rest" | grep -v "^$$pkg$$")"; \
	done; \
	status=0; \
	echo "test-race: $$(echo "$$rest" | wc -w) packages in parallel"; \
	go test -race -timeout $(GO_TEST_TIMEOUT) $$rest || status=1; \
	if [ -n "$$docker" ]; then \
		echo "test-race: $$(echo "$$docker" | wc -w) container packages, $(DOCKER_TEST_PARALLELISM) at a time"; \
		go test -race -p $(DOCKER_TEST_PARALLELISM) -timeout $(GO_TEST_TIMEOUT) $$docker || status=1; \
	fi; \
	exit $$status

# DOCKER_DEPENDENT_PACKAGES is every package whose test files import
# testcontainers-go directly (a real, ephemeral Docker container: NATS,
# sshd, LocalStack, Postgres), verified by grepping every .go file in the
# module for that import rather than assumed or guessed from directory
# names. It was written for Phase 72's CI matrix, whose macOS and Windows
# legs run on GitHub runners that ship no Docker daemon; those legs now
# build and vet only, so the two targets below (test-no-docker,
# test-repeat) are the list's remaining readers, both of them local.
#
# This is a named list, not a glob or a directory-name pattern, on
# purpose: a fragile pattern (skip anything under a path containing
# "container", say) can silently stop covering a package it was never
# meant to exclude, or silently start excluding a new package that
# never needed to be. A named list fails the opposite, safer way. A new
# container-backed test added later and not added here FAILS LOUDLY on
# the non-Docker legs (a real, visible CI failure demanding this list be
# updated) rather than silently never running there at all.
DOCKER_DEPENDENT_PACKAGES := \
	github.com/Subject-Void-LLC/the-pleiades/cmd/controller \
	github.com/Subject-Void-LLC/the-pleiades/cmd/pleiades \
	github.com/Subject-Void-LLC/the-pleiades/cmd/runner \
	github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy \
	github.com/Subject-Void-LLC/the-pleiades/internal/archtest \
	github.com/Subject-Void-LLC/the-pleiades/internal/catalog/cloud/aws/ec2 \
	github.com/Subject-Void-LLC/the-pleiades/internal/catalog/cloud/aws/s3 \
	github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve \
	github.com/Subject-Void-LLC/the-pleiades/internal/election \
	github.com/Subject-Void-LLC/the-pleiades/internal/ent \
	github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate/gen \
	github.com/Subject-Void-LLC/the-pleiades/internal/event \
	github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins \
	github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/aws \
	github.com/Subject-Void-LLC/the-pleiades/internal/lock \
	github.com/Subject-Void-LLC/the-pleiades/internal/meshid \
	github.com/Subject-Void-LLC/the-pleiades/internal/runner \
	github.com/Subject-Void-LLC/the-pleiades/internal/topology \
	github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh \
	github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud \
	github.com/Subject-Void-LLC/the-pleiades/pkg/netconf \
	github.com/Subject-Void-LLC/the-pleiades/tests/e2e

# test-no-docker runs every package NOT in DOCKER_DEPENDENT_PACKAGES, so
# it proves those packages genuinely execute on the machine running it
# rather than merely compiling. It is real conformance evidence where it
# runs (pkg/remoteexec's own suite uses an in-process, real-TCP,
# real-SSH-protocol fake server, no Docker required, so it is not a unit
# test in disguise); the packages it excludes are the ones whose evidence
# needs a Docker daemon, per test-race.
#
# The CI matrix's macOS leg used to run this target, and no longer does:
# that workflow builds and vets on macOS and Windows and runs no tests at
# all (see ci-remote below). So this is now a target for a developer on a
# machine without a working Docker daemon, and for test-repeat below,
# which reuses its exclusion list. Nothing runs it automatically.
test-no-docker:
	@packages="$$(go list ./...)"; \
	for pkg in $(DOCKER_DEPENDENT_PACKAGES); do \
		packages="$$(echo "$$packages" | grep -v "^$$pkg$$")"; \
	done; \
	go test -race -timeout $(GO_TEST_TIMEOUT) $$packages

# test-repeat is the gate against a test that only passes the first time
# it runs in a process. `go test` defaults to -count=1, so every other
# target in this file, and therefore all of CI, has always measured
# exactly one iteration of each test -- which meant a test that registered
# into a process-wide table and never removed the entry passed forever.
# Nine packages were in that state simultaneously when this target was
# written, twenty-four tests across them, two failing as outright panics
# rather than as errors.
#
# -count=3, not 2, and the third iteration is not padding. Two catches a
# test that leaves an entry behind, because the second iteration collides
# with the first. It does NOT catch state that accumulates toward a
# THRESHOLD rather than colliding on first repeat: pkg/remoteexec memoizes
# one Runner per Options for the life of the process, its breaker counts
# consecutive failures with no window and no decay, and one Ping spends
# three attempts against a threshold of five -- so a test whose subject is
# a dial failure passes at -count=1 and -count=2 and fails from -count=3,
# when the error stops naming the dial failure and says "circuit open".
# A gate set at 2 would have been green on a defect this very change had
# to fix, which is the argument for 3 and also the argument against
# reading any particular number as sufficient.
#
# It reuses test-no-docker's package filter rather than declaring a second
# one, for the reason DOCKER_DEPENDENT_PACKAGES' own comment gives: a
# second copy of an exclusion list drifts from the first and nobody
# notices. Container-backed packages are excluded on purpose -- running
# real NATS, sshd and LocalStack containers three times buys noise, not
# evidence, and those are precisely the packages flaky-packages.json
# already documents as timing-sensitive under load.
#
# That reuse is also what lets this be a bare `go test` in push-gate,
# where test-race, test-integration and coverage all go through
# tools/testgate's flaky tolerance instead. Every package
# flaky-packages.json names is inside the filter above, so this target
# cannot reach one and the tolerance would be a no-op here. That is a
# claim about two lists nothing else connects, so it is asserted by
# tools/internal/flakegate's TestEveryFlakyPackageIsExcludedFromTestRepeat
# rather than trusted to stay true.
#
# No -race, deliberately. test-race already covers that axis at -count=1
# over the same code, and the defect class this target exists for
# (process-wide state surviving a test) is not a data race and is not
# made more visible by the detector, only slower.
#
# Cost, measured rather than estimated: 115s against 90s for the same 187
# packages at -count=1. The build is shared, so only the test bodies run
# twice.
test-repeat:
	@packages="$$(go list ./...)"; \
	for pkg in $(DOCKER_DEPENDENT_PACKAGES); do \
		packages="$$(echo "$$packages" | grep -v "^$$pkg$$")"; \
	done; \
	go test -count=3 -timeout $(GO_TEST_TIMEOUT) $$packages

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
#
# Split the same way test-race is, and for the same reason, which bites
# harder here: this is the pass that also stands up tests/e2e's real
# controller and runner binaries against real Postgres and NATS, so it
# asks the most of the daemon of any target in this file. Six packages
# failed in it on 2026-09-13 under the unsplit command, every one of them
# a container package and every one of them already in
# flaky-packages.json. See DOCKER_TEST_PARALLELISM.
# The package lists are built under the integration tag, so tests/e2e
# lands in the container half here and is absent from test-race's,
# exactly as each tag set's own ./... would have resolved it.
test-integration:
	@all="$$(go list -tags integration ./...)"; \
	rest="$$all"; \
	docker=""; \
	for pkg in $(DOCKER_DEPENDENT_PACKAGES); do \
		if echo "$$all" | grep -q "^$$pkg$$"; then docker="$$docker $$pkg"; fi; \
		rest="$$(echo "$$rest" | grep -v "^$$pkg$$")"; \
	done; \
	status=0; \
	echo "test-integration: $$(echo "$$rest" | wc -w) packages in parallel"; \
	go test -tags integration -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$rest || status=1; \
	if [ -n "$$docker" ]; then \
		echo "test-integration: $$(echo "$$docker" | wc -w) container packages, $(DOCKER_TEST_PARALLELISM) at a time"; \
		go test -tags integration -race -count=1 -p $(DOCKER_TEST_PARALLELISM) -timeout $(GO_TEST_TIMEOUT) $$docker || status=1; \
	fi; \
	exit $$status

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

# tidy-check proves go.mod and go.sum are exactly what `go mod tidy`
# produces, without writing either file. It exists because the tree was
# untidy for the whole life of the AWS and serial work and nothing
# noticed: eight modules the code imports directly (the AWS SDK behind
# pkg/awscloud, go.bug.st/serial behind pkg/serialexec, github.com/pin/tftp
# behind pkg/tftpxfer) sat in the indirect block claiming to be incidental
# transitive pickups, which is exactly the state a branch adding new
# direct imports produces when nobody runs tidy. That misleads anyone
# auditing the dependency surface or deciding which upgrades are this
# project's to own.
#
# `-diff` exits non-zero when a change is needed and prints the diff,
# needing no network beyond the module cache, so this is a gate rather
# than a mutation: CI reports the untidiness and a developer runs
# `go mod tidy` themselves.
tidy-check:
	go mod tidy -diff

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
# tools/gencatalog, which rebuilds the pleiades binary and shells out to
# it once per catalog entry: correct, but minutes of work to prove
# something about a different tree. (That directive used to fail outright
# over an already-generated tree, LESSONS_LEARNED.md #69; it now passes
# --skip-existing and is a no-op, so this is a cost argument rather than a
# correctness one.) Second, `go generate` runs a directive in its own
# package's directory, and gendocs writes to repo-root-relative paths:
# invoking it that way wrote a full copy of docs/reference and
# internal/api/wellknown under tools/gendocs/ and left the two trees the
# git diff below inspects untouched. This target passed for as long as it
# regenerated nothing.
docs-gen-check:
	go run ./tools/gendocs
	git diff --exit-code -- docs/reference internal/api/wellknown
	test -z "$$(git ls-files --others --exclude-standard -- docs/reference internal/api/wellknown)"

# helm-lint (tools/helm-lint) renders helm/the-pleiades in every arrangement
# it supports, asserts the rendered containers are hardened (two distinct
# probes, a numeric non-root uid, a read-only root filesystem, no capabilities,
# no hostPath, no :latest and no digest), and asserts the configurations the
# chart is supposed to REFUSE really are refused, message and all. Same shape
# as docs-lint above: a small Go tool over one artifact, no waiver file except
# the single written probe exemption in its own source.
#
# It needs the `helm` binary and deliberately fails rather than skipping when
# it is missing, for the reason its own doc comment gives: a check that quietly
# does nothing where its tool is absent reports a pass that proved nothing.
# Nothing here needs a Kubernetes cluster; `helm template` renders offline.
helm-lint:
	go run ./tools/helm-lint

# ci is the whole gate, and it is now a LOCAL one. -race, not plain test,
# is deliberately included here (not just in a separate target) because
# the Phase 0 item lists `go test -race ./...` as one thing CI must run,
# and splitting it out would make it easy to land a change that only ran
# the non-race target.
#
# It is no longer what .github/workflows/ci.yml runs. That workflow runs
# ci-remote below -- everything on this line EXCEPT test-race, test-repeat,
# test-integration and coverage -- because the four it drops need a real
# Docker daemon for around twenty packages' worth of ephemeral containers
# and have never produced a green result on a hosted runner. The evidence
# those four produce is real and still required; it is produced here,
# before a push, rather than after one. See ci-remote's own comment for
# what that costs and .github/workflows/ci.yml's job comment for the full
# reasoning.
#
# Never make ci itself tolerant of anything: the two targets below
# (push-gate-race, push-gate-integration) exist so that .githooks/pre-push
# can run something more forgiving of known local flakiness without this
# target becoming any less strict.
ci: build devtools vet fmt tidy-check test-race test-repeat test-integration gosec govulncheck coverage docs-lint docs-gen-check helm-lint templ-gen-check
	@echo "ci: all checks passed"

# ci-remote is the subset .github/workflows/ci.yml runs: every check that
# is cheap, deterministic and needs no infrastructure. It is `ci` minus
# exactly four targets -- test-race, test-repeat, test-integration and
# coverage -- and it is written as its own explicit prerequisite list
# rather than as a filter over ci's, because a filter would silently drop
# or silently adopt a target added to ci later, and which of those two
# happened would depend on a name.
#
# The four it omits are the four that provision real containers (NATS,
# sshd, Postgres, LocalStack, Toxiproxy) or run the full suite again to
# measure it. Everything remaining is a compiler, a scanner, a formatter
# or a generator over the checked-out tree.
#
# What this does not prove, stated here as well as in the workflow because
# this is the line someone will read first: ci-remote does not run one
# test. A change that compiles, vets, formats, scans and regenerates
# cleanly passes it while breaking any behavior in this repository. It is
# a smoke gate, not a merge gate; the merge gate is `make ci`, run by a
# human, or `make push-gate` run by .githooks/pre-push.
#
# devtools is kept rather than dropped with the other test-running
# targets: its `go test -tags devtools ./tools/...` pass is seconds long,
# reaches no container, and is the only thing in this file that compiles
# the tag-gated developer commands at all, so dropping it would stop
# building tools/devcert and tools/uidev anywhere in this workflow.
ci-remote: build devtools vet fmt tidy-check gosec govulncheck docs-lint docs-gen-check helm-lint templ-gen-check
	@echo "ci-remote: all checks passed (no tests were run; see this target's comment)"

# push-gate-race and push-gate-integration run through tools/testgate
# instead of a bare `go test`, so a test failure confined to a package
# flaky-packages.json lists (with a written reason) is printed as a
# warning rather than blocking. FAILURE_PATTERNS.md #61 is the incident
# behind this: "the specific package that loses the race changes between
# runs... is the signature of resource contention, not a code defect,"
# discovered because a fully clean `go test ./... -race` run and this
# sandboxed environment's Docker daemon do not reliably coexist once
# enough packages provision real containers at once. A failure OUTSIDE
# flaky-packages.json, or any build failure anywhere, still fails these
# targets exactly like test-race/test-integration do; see
# tools/testgate's own doc comment for the classification rule in full.
push-gate-race:
	go run ./tools/testgate

push-gate-integration:
	go run ./tools/testgate -integration

# push-gate-coverage is coverage's own tolerant counterpart, for the same
# reason push-gate-race/push-gate-integration exist: tools/coverage-check
# runs its own full `go test ./... -cover` internally (measureCoverage),
# entirely separately from test-race/test-integration, so a container- or
# timing-contention failure inside THAT run was still an unconditional
# hard failure even after push-gate-race/push-gate-integration's own
# tolerance was added -- found by running push-gate for real and watching
# it fail here specifically, on a flaky-packages.json package, after both
# test targets above had already passed. -tolerant applies the identical
# flaky-packages.json classification tools/testgate uses; see
# tools/coverage-check's own measureCoverageTolerant doc comment for why a
# package's coverage number is still trustworthy even when one of its
# tests had a tolerated failure.
push-gate-coverage:
	go run ./tools/coverage-check -tolerant

# push-gate is what .githooks/pre-push runs, in place of `make ci`: every
# check ci runs, in the same order, except test-race/test-integration/
# coverage are replaced by their tolerant push-gate-race/
# push-gate-integration/push-gate-coverage counterparts above. GitHub
# Actions never calls this target, only `make ci` directly (see ci's own
# comment above), so nothing here weakens what actually gates a merge; it
# only reduces how much known-flaky local noise a developer has to fight
# through, and re-run, before a push reaches that real gate.
push-gate: build devtools vet fmt tidy-check push-gate-race test-repeat push-gate-integration gosec govulncheck push-gate-coverage docs-lint docs-gen-check helm-lint templ-gen-check
	@echo "push-gate: all checks passed (a warning above, if any, is a known-flaky package from flaky-packages.json, not a blocking failure)"

# templ-gen regenerates the view layer's templates. templ emits a
# _templ.go beside every .templ, and both are committed.
templ-gen:
	go tool templ generate ./internal/ui/render

# templ-gen-check proves the committed generated files match a fresh run,
# mirroring docs-gen-check's own three-part shape: regenerate, diff, and
# then check for newly created files, which git diff alone is blind to.
templ-gen-check: templ-gen
	git diff --exit-code -- internal/ui/render
	test -z "$$(git ls-files --others --exclude-standard -- internal/ui/render)"

# dev-cert writes a throwaway serving certificate into .dev-certs/, which
# is gitignored.
#
# Nothing requires it any more. The controller provisions its own
# self-signed certificate when an operator has configured none, so
# `docker compose up` needs no preparatory command and `make ui-dev`
# generates its own. This target was kept rather than deleted because it
# covers the OTHER arrangement, the one a real deployment uses: a
# certificate handed to the controller through TLS_CERT_FILE and
# TLS_KEY_FILE. That path deserves a way to be exercised locally, and this
# is it:
#
#   make dev-cert
#   TLS_CERT_FILE=$$PWD/.dev-certs/cert.pem \
#     TLS_KEY_FILE=$$PWD/.dev-certs/key.pem ./controller
#
# Invoked by file path because tools/devcert carries //go:build devtools,
# the same convention ui-dev below uses; `go run` on a named file ignores
# build constraints, while `make ci` compiles and vets both tools through
# the devtools target above. It calls internal/tlscert's one generator, the
# same code the controller, `make ui-dev` and tests/e2e all use, rather
# than a fourth lookalike.
#
# The pair lands at mode 0600. It used to land at 0644, because devcert's
# -container-readable flag defaulted to true and this target never said
# otherwise, so `make dev-cert` left a world-readable PRIVATE KEY on disk
# with nobody having asked for one. The flag still exists, because the
# case it covers is real (a container running as another UID cannot read a
# 0600 key through a bind mount), but it is now something you ask for:
#
#   make dev-cert DEV_CERT_FLAGS=-container-readable
DEV_CERT_DIR ?= .dev-certs
DEV_CERT_FLAGS ?=

dev-cert:
	go run tools/devcert/main.go -dir $(DEV_CERT_DIR) $(DEV_CERT_FLAGS)

# ui-dev boots the real controller against a throwaway database and prints
# a sign-in token, so the web UI can be looked at without a cluster. It runs
# the shipped binary rather than a harness: a development server that wired
# its own router could show a UI the real composition root does not serve.
#
# Invoked by file path because tools/uidev carries //go:build devtools,
# which keeps it out of the default build and out of gosec's judgement of
# shipped server code while still letting the devtools target above compile
# and vet it.
#
# Ctrl-C stops it. The controller is started with Pdeathsig, so it cannot
# outlive this process even if the terminal is closed or the task is killed;
# ui-stop below exists for the case where an earlier run predates that.
#
# PLEIADES_UI_ADDR=:8081 make ui-dev  runs a second instance alongside a
# first: each picks its own NATS port and its own container name.
ui-dev:
	go run tools/uidev/main.go

# ui-stop clears anything a previous run left behind.
#
# It is a separate target rather than "make ui-dev stop" because make has no
# positional arguments: a trailing word is parsed as a second target to
# build, so `make ui-dev stop` runs ui-dev and then fails looking for a rule
# named stop. This does the same job with a name make can actually reach.
# The bracket in [t]ools stops the pattern matching this rule's own command
# line -- without it, pkill finds the shell running it and make terminates
# itself, which is a memorable way to learn how pkill -f works.
ui-stop:
	-@pkill -f '[t]ools/uidev/main.go' 2>/dev/null || true
	-@pkill -f '[p]leiades-uidev-.*/controller' 2>/dev/null || true
	-@docker rm -f $$(docker ps -aq --filter name=pleiades-uidev) >/dev/null 2>&1 || true
	-@rm -rf /tmp/pleiades-uidev-* 2>/dev/null || true
	@echo "ui-stop: development server, broker and scratch directories cleared"

# break-glass returns this machine to the state every test assumes it starts
# from: no throwaway kind cluster, no compose project holding a database from
# a previous run, no containers left by a test binary that was killed before
# its cleanup ran.
#
# It is the recovery path for a class of failure that does not look like one.
# Leftover infrastructure does not announce itself; it surfaces later as a
# test failing at whichever assertion touched the stale state, which reads as
# a defect in whatever that assertion was about. Reach for this the moment a
# gate fails in a way that does not match the code you changed.
#
# NOT `docker system prune`, and the tool's own doc comment says why at
# length: prune is defined by what is unused, which is a fact about the daemon
# rather than about this repository, so it takes a developer's long-lived
# clusters and images with exactly the same confidence it takes ours. Every
# removal here is positively attributed to this repository first, and anything
# else is listed and left.
#
# It refuses to run while a test run is live, because cleaning up underneath
# one is how this tool came to exist. Flags:
#
#   make break-glass BREAK_GLASS_FLAGS=-n        say what would go, remove nothing
#   make break-glass BREAK_GLASS_FLAGS=-images   also drop the built images
#   make break-glass BREAK_GLASS_FLAGS=-force    clean anyway, breaking that run
#
# Invoked by file path, like dev-cert above: the tool carries //go:build
# devtools, which keeps a destructive maintenance command out of the default
# build and out of gosec's judgement of shipped code, while `make devtools`
# still compiles and vets it.
BREAK_GLASS_FLAGS ?=

break-glass:
	go run tools/breakglass/main.go $(BREAK_GLASS_FLAGS)

# image-scan scans the two images this repository builds for known
# vulnerabilities in what the BASE IMAGE ships, which is the gap neither Go
# scanner can see.
#
# gosec reads this project's Go source and govulncheck reads its module
# graph. Neither has any view of glibc, OpenSSL, zlib or the CA bundle that
# come from gcr.io/distroless/base-debian12, and those are pinned by digest,
# which means they are frozen and will age. Nothing else in this repository
# would ever notice that the pinned base had acquired a CVE.
#
# DELIBERATELY NOT PART OF ci, for two reasons that are about ci rather than
# about scanning. Nothing in ci builds an image, so there would be nothing
# to scan without adding an image build to every run. And trivy resolves
# findings against a vulnerability database it downloads at first use, so
# wiring it into ci would add a second live-network dependency to a target
# that has exactly one today, on a repository whose documentation promises
# an air-gapped install path.
#
# Run it after building the images:
#
#   docker build -f Dockerfile.controller -t pleiades/controller:dev .
#   make image-scan
#
# IMAGE_SCAN_SEVERITY selects what fails the run. The default stops at HIGH
# and CRITICAL because a distroless base reports a long tail of LOW findings
# in packages nothing in these images calls, and a gate that is always red
# is a gate nobody reads.
IMAGE_SCAN_SEVERITY ?= HIGH,CRITICAL
IMAGE_SCAN_IMAGES ?= pleiades/controller:dev pleiades/runner:dev

image-scan: image-tools
	@for image in $(IMAGE_SCAN_IMAGES); do \
		echo "image-scan: $$image"; \
		trivy image --quiet --scanners vuln --severity $(IMAGE_SCAN_SEVERITY) --exit-code 1 "$$image" || exit 1; \
	done
	@echo "image-scan: no $(IMAGE_SCAN_SEVERITY) vulnerabilities in $(IMAGE_SCAN_IMAGES)"
