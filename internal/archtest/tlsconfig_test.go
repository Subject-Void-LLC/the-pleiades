// This file makes Phase 96d's Pattern Entry Gate enforced rather than
// merely written down.
//
// That gate named one expected rejection: hand-rolling a second
// tls.Config. Its reasoning was that internal/tlscert exists precisely so
// no caller writes one by hand, because a hand-written one is where
// InsecureSkipVerify gets typed and where the version floor gets
// forgotten. Phase 96d's own commit message then claimed that TLSFromEnv
// consumed that helper.
//
// It did not. internal/topology/url.go returned a hand-built
// &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, and the only
// thing in the tree that consumed the helper was a test. Every gate this
// repository runs was green the whole time: it is not a build error, not
// a vet finding, not a coverage drop and not an import-graph violation,
// because importing crypto/tls is entirely legitimate. A claim in a
// commit message is not a control. These two rules are.
//
// The zero-value hazard is worth stating plainly, because it is what the
// second rule is about. A tls.Config with no MinVersion does not get "the
// library's sensible default": crypto/tls reads the zero value as TLS 1.0
// on the serving side. A configuration that forgot the floor and one that
// considered it are indistinguishable at a glance, which is exactly the
// shape of defect a source rule catches and a reviewer does not.
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

// tlsConfigLiteral is one crypto/tls Config composite literal, and
// whether it states a version floor.
type tlsConfigLiteral struct {
	pos            token.Pos
	setsMinVersion bool
}

// tlsConfigLiterals returns every crypto/tls Config composite literal in
// file.
//
// It matches a COMPOSITE LITERAL and nothing else, which is what keeps it
// blind to the three shapes that look similar and are not. That blindness
// is by construction rather than by an exclusion list somebody has to
// maintain:
//
//   - A type reference. `func WithTLS(cfg *tls.Config)` and a struct field
//     `tls *tls.Config` are an *ast.Field over an *ast.StarExpr, never an
//     *ast.CompositeLit, so they are unreachable from here.
//   - A bare bool named InsecureSkipVerify.
//     internal/inventory/syncplugin's Config declares one and three
//     packages set it. This rule keys off the literal's TYPE, never off a
//     field name, so none of them is reachable either. That is the whole
//     reason this is a type rule: a name-keyed matcher would have to
//     distinguish a tls.Config literal from a syncplugin.Config literal,
//     which needs type information this package deliberately does not
//     load.
//   - Some other package's Config. sel.X must be the identifier "tls".
//
// Structural rather than type-checked, following logging_test.go and
// mesh_dial_test.go: loading full type information for every package in
// the module would cost more than everything else here combined. The two
// concessions, stated rather than hidden. An aliased or dot import of
// crypto/tls evades this exactly as it evades natsDials; neither appears
// in the module and both are conspicuous in review in a way a missing
// MinVersion is not. And an ELIDED literal type, as in
// []tls.Config{{MinVersion: ...}}, whose inner CompositeLit carries a nil
// Type, is not matched. TestTLSConfigLiteralsDetectsALiteral locks that
// behavior in rather than leaving it to be discovered.
func tlsConfigLiterals(file *ast.File) []tlsConfigLiteral {
	var found []tlsConfigLiteral
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Config" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "tls" {
			return true
		}
		found = append(found, tlsConfigLiteral{
			pos:            lit.Pos(),
			setsMinVersion: literalStatesKey(lit, "MinVersion"),
		})
		return true
	})
	return found
}

// literalStatesKey reports whether lit names the given field.
//
// Keyed fields only, which is the right reading rather than a limitation:
// crypto/tls.Config is a large struct from another module, so a positional
// literal for it is not something anyone writes, and `go vet`'s
// composites check would object if they did.
func literalStatesKey(lit *ast.CompositeLit, name string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == name {
			return true
		}
	}
	return false
}

// TestOnlyTlscertBuildsAMeshTLSConfig asserts that internal/topology
// constructs no tls.Config of its own, and that internal/tlscert still
// constructs one.
//
// SCOPED TO internal/topology, and the scope is stated honestly rather
// than stretched. A module-wide zero rule is not available and never will
// be: two client configurations deliberately skip verification and carry
// a written gosec waiver for it, two more are SERVER configurations
// carrying Certificates, and pkg/winrmexec cannot import internal at all
// because layering_test.go forbids it. What makes the narrow scope
// sufficient is the rule next door. TestOnlyTopologyDialsNats already
// forbids nats.Connect outside this package, so every connection to the
// mesh is dialed from here, and here is therefore where its TLS would be
// typed.
//
// What it does not cover, said out loud rather than left to be found: a
// composition root could still hand-roll a configuration and pass it to
// topology.WithTLS. Policing the ARGUMENT was considered and rejected on
// mesh_dial_test.go's own reasoning, which retired exactly that shape of
// rule as an arms race against aliased imports, a variable holding the
// value, and a wrapper in a third package. The composition roots are
// covered instead by the version-floor ratchet below and by the two
// subprocess refusal gates in cmd/.
func TestOnlyTlscertBuildsAMeshTLSConfig(t *testing.T) {
	root := moduleRoot(t)
	mesh := filepath.Join(root, "internal", "topology")
	owner := filepath.Join(root, "internal", "tlscert")

	var meshFiles, ownerLiterals int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .git holds no Go source, and .claude can hold other agents'
			// git worktrees: full nested checkouts of this same
			// repository, whose own configurations are not this tree's to
			// govern.
			if name := d.Name(); name == ".git" || name == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		inMesh := strings.HasPrefix(path, mesh+string(filepath.Separator))
		inOwner := strings.HasPrefix(path, owner+string(filepath.Separator))
		if !inMesh && !inOwner {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			// The same transient-scaffolding race mesh_dial_test.go
			// tolerates. FAILURE_PATTERNS.md #88.
			return nil
		}

		literals := tlsConfigLiterals(file)
		if inOwner {
			ownerLiterals += len(literals)
			return nil
		}
		meshFiles++

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, lit := range literals {
			t.Errorf("%s:%d builds a tls.Config. internal/tlscert owns the one client configuration this platform makes, because a second one is where InsecureSkipVerify gets typed and where the TLS 1.2 floor gets forgotten. Call tlscert.ClientConfig(pool), or take the whole thing from ServingCert.TLSClientConfig",
				rel, fset.Position(lit.pos).Line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	if meshFiles == 0 {
		t.Fatal("no Go files were scanned under internal/topology, so this rule examined nothing")
	}
	// The positive control, and the reason this rule is not vacuous. This
	// is a ZERO assertion, so a clean tree and a matcher that went blind
	// produce the identical green tick; nothing else in this test can tell
	// them apart. The owner of the construct must still own it.
	if ownerLiterals == 0 {
		t.Fatal("internal/tlscert contains no tls.Config literal, so the matcher is not matching anything; the owner of this construct must still build one, and if it genuinely no longer does, delete this rule rather than letting it pass vacuously")
	}
}

// TestEveryTLSConfigStatesAVersionFloor asserts that no tls.Config
// constructed anywhere in shipping code leaves MinVersion at its zero
// value.
//
// There are eight of these in the module today, and all eight state it: a
// certificate generator, a mesh client, the API's own serving
// configuration, a container self-probe, a Vault client, a WinRM
// transport, and the two that deliberately skip verification. So this
// lands green and is a pure ratchet. It exists so the ninth has to.
//
// NO ALLOWLIST, deliberately, and for the reason layering_test.go writes
// out about an empty one: a list here would offer the first person who
// trips this rule a place to add themselves instead of one line to type.
// The fix is always the same line, and it is never controversial.
//
// Test files are excluded, on TestOnlyTopologyDialsNats's reasoning: a
// test that stands up a throwaway TLS server to watch how a client
// behaves is asking a question about the library, not configuring this
// product. What that concedes is real and bounded, and gosec with -tests
// is the tool that would cover it.
func TestEveryTLSConfigStatesAVersionFloor(t *testing.T) {
	root := moduleRoot(t)

	var checked, literals int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
			return nil
		}
		checked++

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, lit := range tlsConfigLiterals(file) {
			literals++
			if lit.setsMinVersion {
				continue
			}
			t.Errorf("%s:%d builds a tls.Config with no MinVersion. The zero value is not a sensible default, it is TLS 1.0, which this platform does not accept in either direction. State MinVersion: tls.VersionTLS12, or take the configuration from internal/tlscert, which states it for you",
				rel, fset.Position(lit.pos).Line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	if checked == 0 {
		t.Fatal("no Go files were scanned, so this test proved nothing")
	}
	if literals == 0 {
		t.Fatal("the matcher found no tls.Config literal anywhere in shipping code, so it is matching nothing; if this module genuinely no longer builds one, delete this rule rather than letting it pass vacuously")
	}
}

// TestTLSConfigLiteralsDetectsALiteral is the negative control: it proves
// the matcher really finds a literal, really reads MinVersion, and really
// is not fooled by the five near-misses that would otherwise make it
// decorative or noisy.
//
// It matters more here than for most rules in this package, because
// TestOnlyTlscertBuildsAMeshTLSConfig asserts that a package contains
// ZERO of something. That is the case where "clean tree" and "broken
// matcher" are most completely indistinguishable.
func TestTLSConfigLiteralsDetectsALiteral(t *testing.T) {
	const src = `package probe

import "crypto/tls"

// A type reference in a struct field, beside a bare bool carrying the
// exact name a field-keyed matcher would have tripped on.
type holder struct {
	cfg                *tls.Config
	InsecureSkipVerify bool
}

// Two more type references, in a signature.
func build(in *tls.Config) *tls.Config {
	a := &tls.Config{MinVersion: tls.VersionTLS12}      // a literal, with the floor
	b := tls.Config{RootCAs: pool}                      // a literal, WITHOUT the floor
	c := &othertls.Config{MinVersion: tls.VersionTLS12} // a different package's Config
	d := holder{InsecureSkipVerify: true}               // a bare bool, not a tls.Config
	var e tls.Config                                    // a declaration, not a literal
	f := []tls.Config{{MinVersion: tls.VersionTLS12}}   // an ELIDED inner type: not matched, see tlsConfigLiterals
	_, _, _, _, _, _ = a, b, c, d, e, f
	return in
}`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}

	got := tlsConfigLiterals(file)
	want := []struct {
		line           int
		setsMinVersion bool
	}{
		{line: 14, setsMinVersion: true},
		{line: 15, setsMinVersion: false},
	}
	if len(got) != len(want) {
		lines := make([]int, 0, len(got))
		for _, lit := range got {
			lines = append(lines, fset.Position(lit.pos).Line)
		}
		t.Fatalf("tlsConfigLiterals() reported lines %v, want 2 literals on lines 14 and 15", lines)
	}
	for i, lit := range got {
		if line := fset.Position(lit.pos).Line; line != want[i].line {
			t.Errorf("literal %d reported on line %d, want %d", i, line, want[i].line)
		}
		if lit.setsMinVersion != want[i].setsMinVersion {
			t.Errorf("literal %d setsMinVersion = %t, want %t", i, lit.setsMinVersion, want[i].setsMinVersion)
		}
	}
}
