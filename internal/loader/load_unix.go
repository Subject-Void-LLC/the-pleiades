//go:build unix

// Package loader: Load, the two passes that turn a directory of programs
// into registered Collection methods.
package loader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// candidate is one program that passed its own inspection and described
// itself, waiting for the cross-program checks before anything registers.
type candidate struct {
	path   string
	digest string
	desc   external.Description
	// protocol is the version this build and the program agreed on.
	protocol external.Protocol
	// identity is the program's Collection identity (programIdentity), set
	// by register before any method registers.
	identity wire.CollectionIdentity
}

// Load loads every external Collection program in dir and registers its
// methods, so a runbook can call them by name.
//
// It runs in two passes. Pass one inspects and describes every program
// and validates every method: the directory's and each program's
// ownership and permissions, each program's digest, its description's
// protocol, and each method's status, capabilities, name and engine
// version. Pass two registers every method through collection.Register.
// Any failure in pass one refuses the whole directory: the error names
// every program (and method) refused and why, and nothing is registered.
// A program whose digest is not in the directory's approval list
// (ApprovalFile) is refused before it runs at all, naming the command
// that approves it.
//
// Names beginning with "." are ignored, so an editor's swap file or a
// .keep marker is harmless. Every other entry must be a program; anything
// else is refused rather than skipped.
//
// An empty directory loads nothing and is not an error. Calling Load twice
// in one process refuses the second call's methods, since their names are
// then already registered.
func Load(ctx context.Context, dir string, opts Options) (*Set, error) {
	o := opts.withDefaults()

	if !o.unconfined {
		abi, err := confinementAvailable()
		if err != nil {
			return nil, fmt.Errorf("external collections: %w", err)
		}
		o.abi = abi
	}

	root, err := checkDir(dir)
	if err != nil {
		return nil, fmt.Errorf("external collections: %w", err)
	}
	if err := checkReach(root, o); err != nil {
		return nil, fmt.Errorf("external collections: %w", err)
	}
	// Read before any program runs, even to describe itself: a program no
	// one approved is refused unstarted.
	approvals, err := readApprovals(root)
	if err != nil {
		return nil, fmt.Errorf("external collections: %w", err)
	}
	approved := approvedDigests(approvals)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("external collections: reading %s: %w", root, err)
	}

	// Before the first program starts, make this process's memory and
	// starting environment unreadable to it (protectProcess). A directory
	// with nothing to run leaves the process as it was.
	if !o.unconfined && slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !strings.HasPrefix(e.Name(), ".") }) {
		if err := protectProcess(); err != nil {
			return nil, fmt.Errorf("external collections: %w", err)
		}
	}

	// Pass one. Every problem is collected rather than returned at the
	// first, so an operator fixing a directory sees all of it at once.
	var problems []error
	var found []candidate
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		c, err := inspectAndDescribe(ctx, path, approved, o)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		found = append(found, c)
	}
	warnings, methodProblems := validateCandidates(found, o.EngineVersion, reservedNamespaces())
	problems = append(problems, methodProblems...)
	if len(problems) > 0 {
		return nil, fmt.Errorf("external collections in %s were refused and nothing was registered:\n%w", root, errors.Join(problems...))
	}

	// Pass two.
	programs, err := register(found, o)
	if err != nil {
		return nil, err
	}

	for _, p := range programs {
		o.Logger.Info("loaded external collection", "program", p.Path, "digest", p.Digest, "methods", p.Methods)
	}
	for _, w := range warnings {
		o.Logger.Warn("external collection warning", "warning", w)
	}
	return newSet(programs, warnings), nil
}

// inspectAndDescribe is pass one for a single program: inspect it, pin
// its digest, refuse it unless that digest is approved, and ask it to
// describe itself through the same open file that was hashed.
func inspectAndDescribe(ctx context.Context, path string, approved map[string][]string, o Options) (candidate, error) {
	prog, digest, err := openProgram(path)
	if err != nil {
		return candidate{}, fmt.Errorf("program %s %w", path, err)
	}
	defer func() { _ = prog.Close() }()
	if err := checkApproved(approved, path, digest); err != nil {
		return candidate{}, err
	}
	desc, err := runDescribe(ctx, path, prog, o)
	if err != nil {
		return candidate{}, fmt.Errorf("program %s: %w", path, err)
	}
	// parseDescription already refused a range this build shares nothing
	// with, so this is the agreed version.
	protocol, err := external.Negotiate(desc.Range())
	if err != nil {
		return candidate{}, fmt.Errorf("program %s: %w", path, err)
	}
	return candidate{path: path, digest: digest, desc: desc, protocol: protocol}, nil
}

// validateCandidates is pass one's cross-program half: every method of
// every program is validated on its own (validateMethod), and no name may
// be claimed twice, whether by two programs or twice by one.
//
// A name claimed twice is refused for both claimants. Picking one would
// be exactly the silent choice between two implementations of one name
// that must never happen across a trust boundary, and the order entries
// happen to sort in is no basis for it.
func validateCandidates(found []candidate, running string, reserved map[string]bool) (warnings []string, problems []error) {
	claimedBy := map[string]string{}
	capabilitiesClaimedBy := map[capability.Name]string{}
	factsDeclared := map[string]declaredFact{}
	for _, c := range found {
		problems = append(problems, crossProgramFacts(c, factsDeclared)...)
		defined, capabilityProblems := validateCapabilities(c, reserved, capabilitiesClaimedBy)
		problems = append(problems, capabilityProblems...)
		var unchecked []string
		for _, m := range c.desc.Methods {
			versionUnchecked, err := validateMethod(m, running, reserved, defined)
			if err != nil {
				problems = append(problems, fmt.Errorf("program %s: %w", c.path, err))
				continue
			}
			if first, dup := claimedBy[m.Name]; dup {
				problems = append(problems, fmt.Errorf("method %q is claimed by both %s and %s; neither was loaded", m.Name, first, c.path))
				continue
			}
			claimedBy[m.Name] = c.path
			if versionUnchecked {
				unchecked = append(unchecked, fmt.Sprintf("%s (%s)", m.Name, strings.TrimSpace(m.Manifest.EngineVersion)))
			}
		}
		if len(unchecked) > 0 {
			warnings = append(warnings, developmentBuildWarning(c.path, running, unchecked))
		}
		if outside := factsOutsideNamespace(c.desc.Methods); len(outside) > 0 {
			warnings = append(warnings, fmt.Sprintf("program %s declares facts outside its own namespace: %s; name each after its method's namespace (acme.widgets for an acme.* method), which a later protocol requires",
				c.path, strings.Join(outside, ", ")))
		}
		if c.protocol.Status == external.ProtocolDeprecated {
			warnings = append(warnings, deprecatedProtocolWarning(c.path, c.protocol))
		}
	}
	return warnings, problems
}

// declaredFact is one program's declaration of a fact, kept to compare
// another program's with.
type declaredFact struct {
	path string
	spec collection.FactSpec
}

// crossProgramFacts refuses a fact c declares that an earlier program in the
// directory declares with another kind or type. Register would refuse the
// second program in pass two, after the first had registered, leaving the
// directory loaded halfway; pass one says it first. Facts are compared by key
// (a family by its prefix); a family holding another program's key is left
// to Register.
func crossProgramFacts(c candidate, seen map[string]declaredFact) []error {
	var problems []error
	for _, m := range c.desc.Methods {
		for _, s := range m.Manifest.Facts {
			id := s.Key
			if s.Prefix {
				id += "*"
			}
			prev, ok := seen[id]
			switch {
			case !ok:
				seen[id] = declaredFact{path: c.path, spec: s}
			case prev.path != c.path && (prev.spec.Kind != s.Kind || prev.spec.Type != s.Type):
				problems = append(problems, fmt.Errorf("fact %q is declared %s %s by %s and %s %s by %s; neither was loaded",
					s.Key, prev.spec.Kind, prev.spec.Type, prev.path, s.Kind, s.Type, c.path))
			}
		}
	}
	return problems
}

// factsOutsideNamespace lists, quoted and in order, the fact keys a
// program's methods declare that do not start with the declaring method's
// own namespace and a dot.
func factsOutsideNamespace(methods []external.DescribedMethod) []string {
	var out []string
	for _, m := range methods {
		ns, _, _ := strings.Cut(m.Name, ".")
		for _, s := range m.Manifest.Facts {
			if !strings.HasPrefix(s.Key, ns+".") {
				out = append(out, fmt.Sprintf("%q", s.Key))
			}
		}
	}
	return out
}

// deprecatedProtocolWarning is the one warning a program speaking a
// deprecated protocol gets, once per program on every command, naming the
// release that stops speaking it.
func deprecatedProtocolWarning(path string, p external.Protocol) string {
	return fmt.Sprintf("program %s speaks protocol %d, which is deprecated and removed in %s; rebuild it against a release that speaks a newer one (this build speaks %s)",
		path, p.Version, p.RemovedIn, external.SpokenText())
}

// developmentBuildWarning is the one warning a program gets when this is
// a development build and some of its methods state an engine version:
// one line per program per command, naming each method and what it
// requires, rather than one per method, which on every command is noise
// people learn to skip.
func developmentBuildWarning(path, running string, methods []string) string {
	shown := running
	if shown == "" {
		shown = "unset"
	}
	return fmt.Sprintf("program %s: this is a development build (%s), so the engine versions its methods require were not checked: %s",
		path, shown, strings.Join(methods, ", "))
}

// register is pass two: every method of every candidate is registered
// with a proxy that runs its program. Each method's Check is set only
// when its manifest declares check support, which is the only shape
// collection.Register accepts.
//
// Pass one mirrored every rule Register applies, so a refusal here means
// Register gained a rule pass one does not know, or something else
// registered one of these names in the meantime. Either way the methods
// registered before it cannot be taken back, and the error says which.
func register(found []candidate, o Options) ([]Program, error) {
	// Every program's identity first, so a program that cannot have one is
	// refused before anything registers. Pass one already refused a method
	// named twice, so none is refused here today.
	for i := range found {
		id, err := programIdentity(found[i].path, found[i].desc.Methods)
		if err != nil {
			return nil, fmt.Errorf("external collections: %w", err)
		}
		found[i].identity = id
	}
	var programs []Program
	var registered []string
	for _, c := range found {
		prog := &program{path: c.path, dir: filepath.Dir(c.path), digest: c.digest, opts: o, protocol: c.protocol.Version}
		p := Program{Path: c.path, Digest: c.digest, Protocol: c.protocol.Version}
		// The program's capabilities first, so its methods may require them.
		// The provider is the loader's own name for the program, never the
		// program's description of itself.
		for _, def := range c.desc.Capabilities {
			def.Provider = filepath.Base(c.path)
			if err := capability.RegisterData(def); err != nil {
				return nil, fmt.Errorf("external collections: program %s: defining capability %q failed after validation passed, and %d method(s) registered before it stay registered (%s): %w",
					c.path, def.Name, len(registered), strings.Join(registered, ", "), err)
			}
			p.Capabilities = append(p.Capabilities, string(def.Name))
		}
		for _, m := range c.desc.Methods {
			if err := registerMethod(descriptorFor(prog, c, m)); err != nil {
				return nil, fmt.Errorf("external collections: program %s: registering %q failed after validation passed, and %d method(s) registered before it stay registered (%s): %w",
					c.path, m.Name, len(registered), strings.Join(registered, ", "), err)
			}
			registered = append(registered, m.Name)
			p.Methods = append(p.Methods, m.Name)
		}
		programs = append(programs, p)
	}
	return programs, nil
}

// descriptorFor is the registry entry for one of a program's methods: its
// manifest as described, and functions that run the program. The
// provider is set here, from what the loader itself inspected, and never
// from the program's description. Check is set only when the manifest
// declares check support, the only shape collection.Register accepts.
func descriptorFor(prog *program, c candidate, m external.DescribedMethod) collection.Descriptor {
	d := collection.Descriptor{
		Name:     m.Name,
		Manifest: m.Manifest,
		Invoke:   prog.method(m.Name, collection.ModeExecute),
		Provider: &collection.Provider{Program: c.path, Digest: c.digest, Identity: c.identity},
	}
	if m.Manifest.SupportsCheck {
		d.Check = prog.method(m.Name, collection.ModeCheck)
	}
	return d
}

// registerMethod is collection.Register, as pass two calls it. A variable
// only so a test can give Register a rule pass one does not mirror and
// prove the refusal names every method already registered.
var registerMethod = collection.Register

// Inspect checks the program named program in dir exactly as Load would
// (the directory, the program's file, its reach) and runs its describe,
// confined, whether or not the build is approved yet. It is what
// `pleiades collection approve` shows a person before they approve a
// build: its digest and every method it says it provides. It registers
// nothing and hands the program nothing but the describe command.
func Inspect(ctx context.Context, dir, program string, opts Options) (string, external.Description, error) {
	o := opts.withDefaults()
	if !o.unconfined {
		abi, err := confinementAvailable()
		if err != nil {
			return "", external.Description{}, err
		}
		o.abi = abi
	}
	root, err := checkDir(dir)
	if err != nil {
		return "", external.Description{}, err
	}
	if err := checkReach(root, o); err != nil {
		return "", external.Description{}, err
	}
	if program == "" || strings.ContainsRune(program, filepath.Separator) || strings.HasPrefix(program, ".") {
		return "", external.Description{}, fmt.Errorf("%q is not the file name of a program in %s", program, root)
	}
	path := filepath.Join(root, program)
	prog, digest, err := openProgram(path)
	if err != nil {
		return "", external.Description{}, fmt.Errorf("program %s %w", path, err)
	}
	defer func() { _ = prog.Close() }()
	if !o.unconfined {
		if err := protectProcess(); err != nil {
			return "", external.Description{}, err
		}
	}
	desc, err := runDescribe(ctx, path, prog, o)
	if err != nil {
		return "", external.Description{}, fmt.Errorf("program %s: %w", path, err)
	}
	return digest, desc, nil
}

// ProgramDigest returns the digest of program in dir without running it,
// after the directory and file checks Load and Inspect apply, so a caller
// can tell an approved build from replaced bytes before anything executes.
func ProgramDigest(dir, program string) (string, error) {
	root, err := checkDir(dir)
	if err != nil {
		return "", err
	}
	if program == "" || strings.ContainsRune(program, filepath.Separator) || strings.HasPrefix(program, ".") {
		return "", fmt.Errorf("%q is not the file name of a program in %s", program, root)
	}
	path := filepath.Join(root, program)
	prog, digest, err := openProgram(path)
	if err != nil {
		return "", fmt.Errorf("program %s %w", path, err)
	}
	_ = prog.Close()
	return digest, nil
}
