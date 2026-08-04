// Package archtest asserts the Section 25 layering rules
// (.SPECIFICATION/PLAN.md Section 25's Enforcement note, and the Phase 0
// precondition that names it explicitly): pkg/ never imports internal/,
// only designated adapter packages import a concrete driver, and
// internal/engine imports none. It runs as an ordinary go test so CI
// enforces it on every build, per Section 25's own text ("It lands with
// the first port extraction and runs in CI from that day").
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
const modulePath = "github.com/SubjectVoidLLC/the-pleiades"

// concreteDriverPrefixes are the import path prefixes this test treats as
// "a concrete driver" per Section 25's own Enforcement note, which names
// NATS and ent's SQL driver by example.
var concreteDriverPrefixes = []string{
	"github.com/nats-io/nats.go",
	"github.com/mattn/go-sqlite3",
}

// adapterAllowlist is every internal/ package permitted to import a
// concrete driver directly, reflecting the real, working adapter
// boundaries this repository has built: internal/event and internal/lock
// (Phase W4's NATS-backed event.Bus/lock.Manager adapters), internal/ent
// (the one place ent's SQL driver is opened, embedded.go), internal/api
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
// internal/adapters/native and internal/ansible are deliberately NOT
// listed as of Phase 2: both used to hand-roll their own
// jetstream.JetStream.PublishMsg calls, and this phase moved them onto
// event.Bus.Publish instead (internal/adapters/native/adapter.go,
// internal/ansible/receptor.go), so neither imports a concrete driver
// anymore. TestAdapterAllowlistHasNoStaleEntries is what caught this: it
// failed the moment those two entries became stale, which is exactly the
// live enforcement Section 25 asks for, not a test to appease by leaving
// unused entries in place.
var adapterAllowlist = map[string]bool{
	modulePath + "/internal/api":      true,
	modulePath + "/internal/ent":      true,
	modulePath + "/internal/event":    true,
	modulePath + "/internal/lock":     true,
	modulePath + "/internal/runner":   true,
	modulePath + "/internal/topology": true,
}

// listedPackage is the subset of `go list -json` output this test reads.
type listedPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

// goList runs `go list -json <deps flag> <pattern>` and decodes its
// newline-delimited-JSON-object output (go list's -json emits one JSON
// value per package, concatenated, not a JSON array).
func goList(t *testing.T, withDeps bool, pattern string) []listedPackage {
	t.Helper()

	args := []string{"list", "-json"}
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
	for _, pkg := range goList(t, false, modulePath+"/internal/engine/...") {
		for _, imp := range pkg.Imports {
			if hasPrefix(imp, concreteDriverPrefixes) {
				t.Errorf("%s imports concrete driver %q: internal/engine must depend on a port, never a driver", pkg.ImportPath, imp)
			}
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
