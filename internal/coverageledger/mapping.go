// Package coverageledger: map.yaml, the hand-written half of a ledger, and the
// rules it is held to on its own.
package coverageledger

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Map is a collection's map.yaml.
type Map struct {
	// Ops maps a contract operation's ID to the method that implements it or
	// the reason it is out of scope. An operation not listed is a gap: it
	// counts against the collection's percentage.
	Ops map[string]Mapping `yaml:"ops"`
	// MethodsWithoutOp names, with a reason, a method of the collection that
	// implements no single operation of the contract (one built from several,
	// or from none).
	MethodsWithoutOp map[string]string `yaml:"methods_without_op"`
	// Proof is, per method FQCN, per OS, how the method is proven.
	Proof map[string]map[OS]Proof `yaml:"proof"`
}

// Mapping is what one operation maps to: one or more methods, or an
// exclusion. Exactly one of the two is set.
type Mapping struct {
	// Methods are the FQCNs implementing the operation.
	Methods []string `yaml:"methods"`
	// Excluded is why the operation is out of the collection's scope. An
	// excluded operation counts neither for nor against the percentage, so
	// the reason has to say why no runbook would want it, not that it is
	// hard.
	Excluded string `yaml:"excluded"`
}

// OS is an operating system a method can be proven on.
type OS string

// The operating systems a proof may name.
const (
	OSLinux   OS = "linux"
	OSFreeBSD OS = "freebsd"
	OSDarwin  OS = "darwin"
	OSWindows OS = "windows"
)

// KnownOS reports whether os is one a proof may name.
func KnownOS(os OS) bool {
	switch os {
	case OSLinux, OSFreeBSD, OSDarwin, OSWindows:
		return true
	}
	return false
}

// ProofStatus is how strongly a method is proven on one OS.
type ProofStatus string

// The proof statuses, strongest first.
const (
	// ProofLive is a test against the real thing: a real server, device or
	// tool, reached the way a run reaches it.
	ProofLive ProofStatus = "live"
	// ProofFixture is a test against a stand-in: a fake server, a scripted
	// device, recorded output.
	ProofFixture ProofStatus = "fixture"
	// ProofNone is no test.
	ProofNone ProofStatus = "none"
)

// Proof is how one method is proven on one OS.
type Proof struct {
	// Status is the strength of the proof.
	Status ProofStatus `yaml:"status"`
	// Test names the test, as <module-relative package dir>.<TestName>, for
	// example internal/catalog/virt/clab.TestLive_ARealLabEndToEnd. Required
	// for live and fixture, refused for none.
	Test string `yaml:"test"`
}

// testRef is the shape of Proof.Test.
var testRef = regexp.MustCompile(`^[a-z0-9_./-]+\.Test[A-Za-z0-9_]*$`)

// SplitTest splits a Proof.Test into its package directory and test name.
func SplitTest(ref string) (dir, name string) {
	i := strings.LastIndex(ref, ".")
	return ref[:i], ref[i+1:]
}

// Problems returns every way l breaks the rules a ledger can be checked
// against without the registry or the source tree: each mapped operation is
// in the contract, each mapping is a method or an exclusion with a reason,
// each FQCN named is in the collection, and each proof names a known OS, a
// known status and, when it needs one, a test. Ordered for stable output.
func (l Ledger) Problems() []string {
	c := l.Contract
	var out []string
	add := func(format string, args ...any) { out = append(out, c.Collection+": "+fmt.Sprintf(format, args...)) }

	if c.Title == "" || c.Source.URL == "" || c.Source.Version == "" || len(c.Source.SHA256) != 64 {
		add("contract.json needs a title and a source with url, version and a 64-hex-digit sha256")
	}
	if c.Source.Kind != SourceOpenAPI && c.Source.Kind != SourceCLI {
		add("source kind %q is neither %q nor %q", c.Source.Kind, SourceOpenAPI, SourceCLI)
	}
	if c.Source.Kind == SourceOpenAPI && c.Source.File == "" {
		add("an openapi contract is extracted from a vendored file, and source.file names none")
	}
	known := map[string]bool{}
	for _, op := range c.Ops {
		if op.ID == "" || known[op.ID] {
			add("operation %q is empty or listed twice", op.ID)
		}
		known[op.ID] = true
	}
	inCollection := func(fqcn string) bool { return strings.HasPrefix(fqcn, c.Collection+".") }

	for _, id := range sortedKeys(l.Map.Ops) {
		m := l.Map.Ops[id]
		switch {
		case !known[id]:
			add("map.yaml maps %q, which the contract does not define", id)
		case (len(m.Methods) == 0) == (strings.TrimSpace(m.Excluded) == ""):
			add("operation %q needs exactly one of methods or excluded (with its reason)", id)
		}
		for _, fqcn := range m.Methods {
			if !inCollection(fqcn) {
				add("operation %q maps to %s, which is not in the collection", id, fqcn)
			}
		}
	}
	for _, fqcn := range sortedKeys(l.Map.MethodsWithoutOp) {
		if !inCollection(fqcn) || strings.TrimSpace(l.Map.MethodsWithoutOp[fqcn]) == "" {
			add("methods_without_op %s is not in the collection or gives no reason", fqcn)
		}
	}
	for _, fqcn := range sortedKeys(l.Map.Proof) {
		if !inCollection(fqcn) {
			add("proof for %s, which is not in the collection", fqcn)
		}
		for _, os := range sortedKeys(l.Map.Proof[fqcn]) {
			p := l.Map.Proof[fqcn][os]
			switch {
			case !KnownOS(os):
				add("proof for %s names OS %q", fqcn, os)
			case p.Status == ProofNone && p.Test != "":
				add("proof for %s on %s is none but names a test", fqcn, os)
			case (p.Status == ProofLive || p.Status == ProofFixture) && !testRef.MatchString(p.Test):
				add("proof for %s on %s is %s and needs a test written <package dir>.<TestName>, not %q", fqcn, os, p.Status, p.Test)
			case p.Status != ProofLive && p.Status != ProofFixture && p.Status != ProofNone:
				add("proof for %s on %s has status %q", fqcn, os, p.Status)
			}
		}
	}
	return out
}

// sortedKeys returns m's keys in order.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
