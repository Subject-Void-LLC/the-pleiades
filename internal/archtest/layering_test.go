// Package archtest asserts the Section 25 layering rules
// (.SPECIFICATION/PLAN.md Section 25's Enforcement note, and the Phase 0
// precondition that names it explicitly): pkg/ never imports internal/,
// only designated adapter packages import a concrete driver, and
// internal/engine imports none, nor this module's own generated
// persistence layer. It runs as an ordinary go test so CI enforces it on
// every build, per Section 25's own text ("It lands with the first port
// extraction and runs in CI from that day").
//
// This lives in its own package rather than inside internal/engine or
// pkg/inventory because it is cross-cutting infrastructure owned by no
// single phase, the same reason Phase 0 itself is not owned by any single
// phase.
package archtest

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// modulePath is this repository's own module path (go.mod), used to tell
// this module's packages apart from third-party ones in a dependency
// list.
const modulePath = "github.com/Subject-Void-LLC/the-pleiades"

// concreteDriverPrefixes are the import path prefixes this test treats as
// "a concrete driver" per Section 25's own Enforcement note, which names
// NATS and ent's SQL driver by example. testcontainers-go joined this list
// in Phase 17 (Legacy Ansible Adapter): internal/adapters/legacy's own
// DockerOrchestrator is this repository's first *production* (not
// test-only) consumer of it, spinning up the real ephemeral container an
// unconverted Ansible playbook runs inside. Every other package in this
// module that imports testcontainers-go does so only from a _test.go
// file, which go list's own Imports field (unlike Deps) never reports, so
// adding this prefix does not affect them.
// lib/pq joined this list in Phase 18 (The Grand Integration Test), which
// gave internal/ent a real PostgreSQL adapter behind OpenDatabase. Until
// then lib/pq was a test-only dependency of this module, so listing it
// would have caught nothing; a Walk-tier deployment is the first thing
// that needs it at run time.
var concreteDriverPrefixes = []string{
	"github.com/nats-io/nats.go",
	"github.com/mattn/go-sqlite3",
	"github.com/lib/pq",
	"github.com/testcontainers/testcontainers-go",
}

// adapterAllowlist is every internal/ package permitted to import a
// concrete driver directly, reflecting the real, working adapter
// boundaries this repository has built: internal/event and internal/lock
// (Phase W4's NATS-backed event.Bus/lock.Manager adapters), internal/ent
// (the one place a SQL driver is opened: open.go resolves a DSN to a
// dialect, and open_sqlite.go/open_postgres.go are the two adapters
// behind it), internal/api
// (LogStreamer's raw per-viewer jetstream.Consumer, PLAN.md Section 26.4's
// deliberate exception to going through Bus.Subscribe),
// internal/runner (the pull-based dispatch loop, PATTERNS.md's own "Push
// vs Pull Execution Model" entry), and internal/topology (Section 25's
// "Messaging topology owner" primitive itself: it is the one place
// jetstream.StreamConfig/ConsumerConfig/KeyValueConfig shapes are
// declared, so every other adapter can depend on topology instead of the
// driver directly). cmd/ composition roots are deliberately not subject
// to this check at all: wiring a concrete implementation into an
// interface is the composition root pattern's whole purpose (Phase W1's
// own Pattern Entry Gate). Adding an entry here is a real design
// decision, not a place to silence a failing test; removing the concrete
// import is usually the right fix instead.
//
// internal/adapters/native and the former internal/ansible were
// deliberately NOT listed as of Phase 2: both used to hand-roll their own
// jetstream.JetStream.PublishMsg calls, and this phase moved them onto
// event.Bus.Publish instead (internal/adapters/native/adapter.go, the
// former internal/ansible/receptor.go), so neither imported a concrete
// driver anymore. TestAdapterAllowlistHasNoStaleEntries is what caught
// this: it failed the moment those two entries became stale, which is
// exactly the live enforcement Section 25 asks for, not a test to appease
// by leaving unused entries in place.
//
// internal/adapters/legacy joined this list in Phase 17 (Legacy Ansible
// Adapter, the renamed and now-real successor to internal/ansible): it is
// the one designated place testcontainers-go's real Docker client is
// opened, to spin up the ephemeral container an unconverted Ansible
// playbook runs inside (PLAN.md Section 23), the identical
// concrete-driver-behind-a-named-adapter shape internal/event and
// internal/lock already hold for NATS.
var adapterAllowlist = map[string]bool{
	modulePath + "/internal/adapters/legacy": true,
	modulePath + "/internal/api":             true,
	modulePath + "/internal/ent":             true,
	modulePath + "/internal/event":           true,
	modulePath + "/internal/lock":            true,
	modulePath + "/internal/runner":          true,
	modulePath + "/internal/topology":        true,
}

// entPackage is this module's generated persistence layer: internal/ent
// itself plus every package generated beneath it from internal/ent/schema
// by `go generate ./internal/ent`.
const entPackage = modulePath + "/internal/ent"

// listedPackage is the subset of `go list -json` output this test reads.
type listedPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string

	// TestImports and XTestImports are what the package's own tests
	// import, in package and as package_test. Only rules that cover test
	// code read them.
	TestImports  []string
	XTestImports []string

	// Error is go list -e's per-package load failure, populated instead
	// of aborting the whole listing. goList drops any package carrying
	// one; see its own comment for why that is safe here.
	Error *listedPackageError
}

// listedPackageError is the shape go list -json puts in a package's
// Error field. Only the message is kept: nothing here inspects it, and
// it exists so the field can be distinguished from absent.
type listedPackageError struct {
	Err string
}

// goList runs `go list -json -e <deps flag> <pattern>` and decodes its
// newline-delimited-JSON-object output (go list's -json emits one JSON
// value per package, concatenated, not a JSON array). Packages that
// failed to load are dropped rather than inspected.
//
// The -e is load-bearing, and the reason is a real intermittent CI
// failure rather than caution. Several tests in this module generate a
// scaffolded package into the live repository tree and delete it again
// on cleanup, because the generated starter test imports the package by
// its own internal/... path and so genuinely cannot compile from a
// throwaway module outside this one (internal/forge/collectionscaffold,
// internal/inventory/devicescaffold, tools/gencatalog and cmd/pleiades
// all do this, each under its own process-unique directory name). `go
// test ./...` runs packages in parallel, so one of those packages can be
// mid-create or mid-delete at the exact moment this listing walks the
// tree, and without -e a single such directory aborts the entire listing:
//
//	go list -json -deps github.com/...: exit status 1
//	    cannot find package "." in:
//	    	.../internal/catalog/test/relgate62784
//
// which fails an architecture test for a reason that has nothing to do
// with architecture, on a schedule nobody controls.
//
// Dropping unloadable packages costs no real coverage. `make ci` runs
// `build` and `vet` over the whole module before it runs any test, and
// both fail loudly on a package that genuinely does not load, so a
// committed package can never reach this point broken. A package that
// fails to load *here* and nowhere else is by construction one of the
// transient scaffolding directories above.
func goList(t *testing.T, withDeps bool, pattern string) []listedPackage {
	t.Helper()

	args := []string{"list", "-json", "-e"}
	if withDeps {
		args = append(args, "-deps")
	}
	args = append(args, pattern)

	cmd := exec.Command("go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	var pkgs []listedPackage
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		if p.Error != nil {
			// A transient scaffolding directory caught mid-write or
			// mid-delete by a concurrently running test; see this
			// function's own comment.
			continue
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// hasPrefix reports whether importPath starts with any of prefixes.
func hasPrefix(importPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(importPath, p) {
			return true
		}
	}
	return false
}

// isEntPackage reports whether importPath is internal/ent itself or one of
// the packages generated beneath it.
//
// This is an exact match plus a slash-terminated prefix rather than a
// hasPrefix call against "/internal/ent", because that bare prefix would
// also match a future "/internal/entity" and refuse a package the rule was
// never about. A prohibition that fires on the wrong package is one people
// route around rather than honor.
func isEntPackage(importPath string) bool {
	return importPath == entPackage || strings.HasPrefix(importPath, entPackage+"/")
}

// TestPkgNeverImportsInternal asserts pkg/'s packages carry no dependency,
// direct or transitive, on anything under internal/. pkg/ is this
// repository's own extensibility surface (PLAN.md's "anyone can add a new
// concrete type" promise); a dependency on internal/ would mean a
// third-party consumer of pkg/ transitively needs code that is not
// importable outside this module at all.
func TestPkgNeverImportsInternal(t *testing.T) {
	for _, pkg := range goList(t, true, modulePath+"/pkg/...") {
		for _, dep := range pkg.Deps {
			if strings.HasPrefix(dep, modulePath+"/internal/") {
				t.Errorf("%s depends on %s: pkg/ must never import internal/", pkg.ImportPath, dep)
			}
		}
	}
}

// TestOnlyDesignatedAdaptersImportConcreteDrivers asserts every internal/
// package directly importing a concrete driver (NATS, the SQL driver
// ent's embedded store opens) is on the allowlist above.
func TestOnlyDesignatedAdaptersImportConcreteDrivers(t *testing.T) {
	for _, pkg := range goList(t, false, modulePath+"/internal/...") {
		for _, imp := range pkg.Imports {
			if !hasPrefix(imp, concreteDriverPrefixes) {
				continue
			}
			if !adapterAllowlist[pkg.ImportPath] {
				t.Errorf("%s imports concrete driver %q but is not in the adapter allowlist (archtest's adapterAllowlist)", pkg.ImportPath, imp)
			}
		}
	}
}

// TestEngineImportsNoConcreteDriver asserts internal/engine, the core
// business-logic package Section 25 names explicitly, imports neither
// NATS nor a SQL driver: every dependency it has on a concrete medium
// must arrive through a port (ActionExecutor, lock.Manager, event.Bus),
// never a direct import.
func TestEngineImportsNoConcreteDriver(t *testing.T) {
	// The same empty-listing guard TestEngineNeverImportsPersistence
	// carries, and for the same reason. The two rules read the identical
	// pattern, so they must not disagree about what zero packages means:
	// one treating it as proof of a clean tree while the other treats it
	// as a misaimed query is a contradiction a reader would resolve by
	// trusting whichever ran last.
	pkgs := goList(t, false, modulePath+"/internal/engine/...")
	if len(pkgs) == 0 {
		t.Fatalf("go list matched no packages under %s/internal/engine/..., so this rule examined nothing", modulePath)
	}
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if hasPrefix(imp, concreteDriverPrefixes) {
				t.Errorf("%s imports concrete driver %q: internal/engine must depend on a port, never a driver", pkg.ImportPath, imp)
			}
		}
	}
}

// TestEngineNeverImportsPersistence asserts that no package under
// internal/engine imports internal/ent directly.
//
// Phase 40's run journal declares the sink as a Port in internal/engine
// with its Adapter in internal/journal, and the stated reason for that
// split is that internal/engine may not import internal/ent. Nothing
// enforced it. TestEngineImportsNoConcreteDriver above checks
// internal/engine's direct imports against concreteDriverPrefixes, which
// names NATS and the two SQL drivers and does not name this module's own
// generated ORM layer, so an executor that opened an *ent.Client and wrote
// journal rows itself would have passed every architecture test here. The
// port would survive that: it would still exist, still default to a no-op
// sink, and still be bypassed by the one caller that matters.
//
// The check is DIRECT imports, deliberately. The transitive reach is the
// accepted state, not an oversight: internal/engine imports
// internal/inventory, which imports internal/ent for its ent-backed
// repository, so internal/engine has reached the generated packages
// through the module graph since long before this rule existed. A
// transitive check would therefore fail on the commit that introduced it,
// and the only fix would be splitting internal/inventory's domain types
// from its storage adapter, which is a real design decision this rule has
// no mandate to force. A rule that fails the day it lands is not enforced,
// it is waived.
//
// What the direct check still buys is the regression shape that matters. A
// direct import is the only way an ent symbol becomes nameable inside
// internal/engine, so it is the only way an *ent.Client reaches a struct
// field, a parameter or a return type there. Transitive reach through
// internal/inventory hands the executor no name it can write a row with.
//
// There is no allowlist here and no companion staleness test, and that is
// a decision rather than an omission. adapterAllowlist exists because real
// adapter packages legitimately do open a driver, so the list earns its
// keep as a true record of which ones. Nothing under internal/engine
// legitimately needs internal/ent today: the package is ports all the way
// down, and the journal's Adapter lives in internal/journal for exactly
// that reason. An empty allowlist plus a test proving it stays empty is
// ceremony around a bare prohibition, and worse, it offers the first
// person who trips this rule a list to add themselves to instead of an
// adapter to write. If a legitimate case ever turns up, copy
// adapterAllowlist and its TestAdapterAllowlistHasNoStaleEntries pairing
// then, with the real case in hand to write the reason from.
func TestEngineNeverImportsPersistence(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/internal/engine/...")
	// An empty listing and a clean one are the same green tick here, and
	// they mean opposite things. goList already fails when `go list`
	// itself errors, but a pattern that stops matching (a rename, a build
	// tag, every package dropping out as unloadable) returns zero
	// packages and this rule then reports PASS having examined nothing.
	// AGENTS.md's IDE & LSP section names exactly this: an empty result
	// from a correctly aimed query and one from a misaimed query look
	// identical, so reporting the second as evidence of absence is the
	// same failure as claiming an untested path works.
	if len(pkgs) == 0 {
		t.Fatalf("go list matched no packages under %s/internal/engine/..., so this rule examined nothing", modulePath)
	}
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if !isEntPackage(imp) {
				continue
			}
			t.Errorf(
				"%s imports %s directly: internal/engine must reach persistence through a port.\n"+
					"Phase 40's journal sink is engine.Journal, whose concrete store lives in internal/journal "+
					"and is wired in a cmd/ composition root, which is the same shape TargetResolver, "+
					"lock.Manager and event.Bus already use.\n"+
					"Importing the generated layer here collapses that seam and puts an *ent.Client inside the "+
					"one package Section 25 names as depending on no concrete medium at all.",
				pkg.ImportPath, imp)
		}
	}
}

// TestAdapterAllowlistHasNoStaleEntries fails if a package on the
// allowlist no longer actually imports a concrete driver, so the
// allowlist stays a true record of where these dependencies really are
// rather than accumulating permissions nothing uses anymore.
func TestAdapterAllowlistHasNoStaleEntries(t *testing.T) {
	seen := make(map[string]bool, len(adapterAllowlist))
	for _, pkg := range goList(t, false, modulePath+"/internal/...") {
		for _, imp := range pkg.Imports {
			if hasPrefix(imp, concreteDriverPrefixes) {
				seen[pkg.ImportPath] = true
			}
		}
	}

	stale := make([]string, 0)
	for pkg := range adapterAllowlist {
		if !seen[pkg] {
			stale = append(stale, pkg)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("adapterAllowlist entries that no longer import a concrete driver: %v", stale)
	}
}
