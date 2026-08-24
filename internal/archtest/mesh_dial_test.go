// This file makes Phase 96a's single-owner claim for NATS dial options
// enforced rather than merely written down.
//
// The defect it exists for was measured, not imagined. Every dial in this
// module used to be a bare nats.Connect(url) carrying zero options, which
// inherits nats.go's defaults: MaxReconnect 60 at ReconnectWait 2s, and
// RetryOnFailedConnect false. Against a real broker behind a real
// Toxiproxy, a hard TCP cut killed the connection permanently after 2m3s
// and it never came back, however healthy the link became afterwards. A
// Runner that lost its link for two minutes was dead until a human
// restarted the process, and it said nothing at all while that was true:
// no site set a DisconnectErrHandler either.
//
// That class was invisible to every gate this repository runs. It is not
// a build error, not a vet finding, not a coverage drop, and not an
// import-graph violation: all five sites imported nats.go entirely
// legitimately. The only thing that reaches the author of a sixth dial in
// time is a failing test, which is the same argument logging_test.go
// makes for its own existence.
//
// The rule is source inspection rather than a runtime check for the
// reason logging_test.go gives about loggers, and it applies here with
// more force: there is no runtime moment at which "every connection in
// the deployment was dialed correctly" is observable, because each binary
// dials its own in its own main, and the one that forgot is the one no
// test happens to run.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyTopologyDialsNats asserts that internal/topology is the only
// package in shipping code that calls nats.Connect at all.
//
// This started life as the weaker rule "every nats.Connect must spread
// topology.DialOptions", and that rule had a hole worth recording. Its
// matcher checked only that the final argument was a spread of a call to
// something NAMED DialOptions, with no check on the qualifier, so a local
// `func DialOptions() []nats.Option { return nil }` plus
// `nats.Connect(url, DialOptions()...)` reproduced the exact pre-96a
// defect in two lines while passing the guard. Tightening the name check
// would have been an arms race against aliased imports, dot imports, a
// variable holding the function, and a wrapper in a third package.
//
// Forbidding the call outright ends the argument instead. There is
// nothing to spell correctly and nothing to alias around: a package that
// wants a NATS connection calls topology.Connect, which is the only place
// the options and the mandatory post-connect wait are paired.
//
// Test files are excluded, on the reasoning
// TestOnlyTopologyDeclaresJetStreamShapes states for JetStream shape
// literals: a test that dials a throwaway broker to observe how the
// driver behaves is asking a question about the driver, not configuring
// this product's mesh. That exclusion is load-bearing here rather than
// cosmetic, because most of the module's nats.Connect calls are in
// _test.go files.
func TestOnlyTopologyDialsNats(t *testing.T) {
	root := moduleRoot(t)
	owner := filepath.Join(root, "internal", "topology")

	var checked, ownerDials int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .git holds no Go source, and .claude can hold other agents'
			// git worktrees: full nested checkouts of this same
			// repository, whose own dials are not this tree's to govern.
			if name := d.Name(); name == ".git" || name == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			// A transient scaffolding directory caught mid-write by a
			// concurrently running generator test, the same case goList's
			// own -e flag exists for. FAILURE_PATTERNS.md #88 is this race.
			return nil
		}
		checked++

		dials := natsDials(file)
		if strings.HasPrefix(path, owner+string(filepath.Separator)) {
			ownerDials += len(dials)
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, pos := range dials {
			t.Errorf("%s:%d calls nats.Connect directly: internal/topology owns every dial, because the options and the post-connect wait are one contract and a caller that takes half of it gets a connection that dies permanently after about two minutes (Phase 96a's D1) or a handle that is not connected yet (D2). Call topology.Connect(ctx, url, logger, component) instead",
				rel, fset.Position(pos).Line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	if checked == 0 {
		t.Fatal("no Go files were scanned, so this test proved nothing")
	}
	// A rule that finds no dials anywhere, including the owner's own, is
	// green for the wrong reason: a renamed import or a matcher that
	// stopped matching looks identical to a clean tree.
	if ownerDials == 0 {
		t.Fatal("internal/topology contains no nats.Connect call, so the matcher is not matching anything; if the mesh genuinely no longer dials NATS, delete this rule rather than letting it pass vacuously")
	}
}

// natsDials returns the position of every nats.Connect call in file.
//
// It is a separate function so the rule can run against source this test
// controls, which is what TestNatsDialsDetectsADial below does: a walk
// that finds nothing and a matcher that recognizes nothing produce the
// same green result.
//
// The match is structural rather than type-checked, following
// logging_test.go: loading full type information for every package in the
// module would cost more than everything else in this package combined,
// and the failure mode of the cheap version is a false pass on a
// deliberately-named decoy, which nobody writes by accident. Note what
// that concedes: an aliased import (`import n "github.com/nats-io/nats.go"`)
// or a dot import would evade this. Neither appears in the module, both
// are conspicuous in review in a way a missing option is not, and the
// stricter alternative costs a full type-check of the tree.
func natsDials(file *ast.File) []token.Pos {
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Connect" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "nats" {
			return true
		}
		found = append(found, call.Pos())
		return true
	})
	return found
}

// TestNatsDialsDetectsADial is the negative control: it proves the
// matcher really finds a dial, and really is not fooled by the two
// near-misses that would otherwise make it either decorative or noisy.
//
// It parses a source string rather than un-wiring a real call site.
// Phase 73's own guard was negative-controlled by temporarily editing the
// real tree, which is a real control but a manual one that runs once and
// leaves no trace; this one runs on every CI job.
func TestNatsDialsDetectsADial(t *testing.T) {
	const src = `package probe

import "github.com/nats-io/nats.go"

func dials() {
	_, _ = nats.Connect(url)                                     // a dial
	_, _ = nats.Connect(url, topology.DialOptions(l, "x")...)    // still a dial: the rule is WHO, not HOW
	_, _ = somethingelse.Connect(url)                            // not the driver
	_, _ = nats.Drain(url)                                       // not a dial
	_ = nats.Connect                                             // a reference, not a call
}`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}

	got := natsDials(file)
	wantLines := []int{6, 7}
	if len(got) != len(wantLines) {
		lines := make([]int, 0, len(got))
		for _, pos := range got {
			lines = append(lines, fset.Position(pos).Line)
		}
		t.Fatalf("natsDials() reported lines %v, want %v", lines, wantLines)
	}
	for i, pos := range got {
		if line := fset.Position(pos).Line; line != wantLines[i] {
			t.Fatalf("dial %d reported on line %d, want %d", i, line, wantLines[i])
		}
	}
}
