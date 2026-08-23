// This file makes internal/topology's ownership claim enforced rather
// than merely written down.
//
// internal/topology's own package doc calls it "the single owner of every
// NATS JetStream subject, stream, consumer and bucket shape," and
// layering_test.go's adapterAllowlist comment repeats it: topology "is
// the one place jetstream.StreamConfig/ConsumerConfig/KeyValueConfig
// shapes are declared, so every other adapter can depend on topology
// instead of the driver directly." Both sentences were false when they
// were written. internal/lock built its own jetstream.KeyValueConfig
// inline for the "Pleiades_Locks" bucket, which is the bucket that
// carries every leader-election lease and every per-device execution
// lease, and which cmd/controller and cmd/runner each reshape on startup
// through CreateOrUpdateKeyValue.
//
// Several composition roots reshaping one JetStream object at startup is
// this codebase's deliberate pattern, not the defect: topology.EnsureStream
// does exactly that for the main stream from three roots. What makes it
// safe is that the shape is written down once. An inline literal in
// another package is the thing that can drift, and across a rolling
// upgrade of two separately-built images it genuinely could: a lowered
// bucket TTL lands as a lowered stream MaxAge, which expires live lock
// entries and lets two runners execute against one device.
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

// jetstreamShapeTypes are the driver configuration structs whose literals
// belong to internal/topology alone. They are matched by selector name
// (jetstream.StreamConfig and friends), which is enough because the
// import that would make the name resolve to anything else is itself
// forbidden by TestOnlyAdaptersImportConcreteDrivers.
var jetstreamShapeTypes = map[string]bool{
	"StreamConfig":   true,
	"ConsumerConfig": true,
	"KeyValueConfig": true,
}

// TestOnlyTopologyDeclaresJetStreamShapes proves no package outside
// internal/topology writes a JetStream configuration literal.
//
// Test files are excluded deliberately, not overlooked. A test that
// stands up a throwaway stream to observe how the driver behaves is
// asking a question about the driver, not declaring this product's
// topology, and forbidding that would push those tests into contortions
// for no safety gained. What ships is what this rule governs.
func TestOnlyTopologyDeclaresJetStreamShapes(t *testing.T) {
	root := moduleRoot(t)
	owner := filepath.Join(root, "internal", "topology")

	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .git holds no Go source, and .claude can hold other agents'
			// git worktrees: full nested checkouts of this same
			// repository, whose own copy of internal/lock would be
			// reported against a path this rule cannot reason about. The
			// Makefile's fmt target already excludes it for the identical
			// reason.
			if name := d.Name(); name == ".git" || name == ".claude" {
				return filepath.SkipDir
			}
			if path == owner {
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
			// own -e flag exists for.
			return nil
		}
		checked++

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, name := range jetstreamShapeLiterals(file) {
			t.Errorf("%s declares a jetstream.%s literal: every JetStream stream, consumer and bucket shape belongs to internal/topology, so several composition roots provisioning the same object cannot disagree about it -- add a constructor there and call it from here",
				rel, name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	if checked == 0 {
		t.Fatal("no Go files were scanned, so this test proved nothing")
	}
}

// jetstreamShapeLiterals returns the name of every jetstream
// configuration struct literal in file.
//
// It is a separate function so the rule can run against source this test
// controls, which is what TestJetstreamShapeLiteralsDetectsALiteral below
// does: a walk that finds nothing and a matcher that recognizes nothing
// produce the same green result.
func jetstreamShapeLiterals(file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "jetstream" {
			return true
		}
		if jetstreamShapeTypes[sel.Sel.Name] {
			found = append(found, sel.Sel.Name)
		}
		return true
	})
	return found
}

// TestJetstreamShapeLiteralsDetectsALiteral is the negative control: it
// proves the matcher really finds a configuration literal, and really
// ignores a literal from another package that happens to share a type
// name.
func TestJetstreamShapeLiteralsDetectsALiteral(t *testing.T) {
	const src = `package probe

import "github.com/nats-io/nats.go/jetstream"

func build() {
	_ = jetstream.KeyValueConfig{Bucket: "Probe"}
	_ = jetstream.StreamConfig{Name: "Probe"}
	_ = somethingelse.KeyValueConfig{Bucket: "not the driver's"}
	_ = jetstream.PubAck{}
}`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}

	got := jetstreamShapeLiterals(file)
	want := []string{"KeyValueConfig", "StreamConfig"}
	if len(got) != len(want) {
		t.Fatalf("jetstreamShapeLiterals() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("jetstreamShapeLiterals() = %v, want %v", got, want)
		}
	}
}
