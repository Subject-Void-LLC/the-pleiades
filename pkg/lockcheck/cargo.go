// Cargo's Cargo.lock (versions 3 and 4) against Cargo.toml.
package lockcheck

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// cargoFormat is the Cargo plug-in.
type cargoFormat struct{}

func (cargoFormat) Name() string         { return "cargo" }
func (cargoFormat) LockFile() string     { return "Cargo.lock" }
func (cargoFormat) ManifestFile() string { return "Cargo.toml" }
func (cargoFormat) DefaultSources() []string {
	return []string{cratesIOGit, cratesIOSparse}
}

// The two spellings of the crates.io registry a Cargo.lock uses.
const (
	cratesIOGit    = "registry+https://github.com/rust-lang/crates.io-index"
	cratesIOSparse = "sparse+https://index.crates.io/"
)

// cargoLock is the part of Cargo.lock read here.
type cargoLock struct {
	Version int            `toml:"version"`
	Package []cargoPackage `toml:"package"`
}

// cargoPackage is one [[package]] of Cargo.lock.
type cargoPackage struct {
	Name     string `toml:"name"`
	Version  string `toml:"version"`
	Source   string `toml:"source"`
	Checksum string `toml:"checksum"`
}

// cargoManifest is the part of Cargo.toml read here.
type cargoManifest struct {
	Dependencies      map[string]any `toml:"dependencies"`
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
	Target            map[string]struct {
		Dependencies      map[string]any `toml:"dependencies"`
		DevDependencies   map[string]any `toml:"dev-dependencies"`
		BuildDependencies map[string]any `toml:"build-dependencies"`
	} `toml:"target"`
	Workspace *struct {
		Dependencies map[string]any `toml:"dependencies"`
	} `toml:"workspace"`
}

// Verify checks a Cargo.lock against its Cargo.toml.
//
// Sources are Cargo's own source strings (registry+..., sparse+...), plus
// "git" for a git dependency. A local path crate has no source and is not
// checked as one. A workspace member's own manifest is checked by naming
// it, with the workspace's Cargo.lock, in its own call.
func (f cargoFormat) Verify(lockData, manifestData []byte, sources []string) ([]Finding, error) {
	var lock cargoLock
	if err := toml.Unmarshal(lockData, &lock); err != nil {
		return nil, fmt.Errorf("Cargo.lock is not valid TOML: %w", err)
	}
	if lock.Version != 3 && lock.Version != 4 {
		return nil, fmt.Errorf("Cargo.lock is version %d; this reads versions 3 and 4 (Cargo 1.53 and later write them)", lock.Version)
	}
	var manifest cargoManifest
	if err := toml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("Cargo.toml is not valid TOML: %w", err)
	}
	if len(sources) == 0 {
		sources = f.DefaultSources()
	}

	locked := map[string]bool{}
	for _, p := range lock.Package {
		locked[p.Name] = true
	}
	var findings []Finding
	declared := map[string]string{}
	add := func(table string, deps map[string]any) {
		for key, spec := range deps {
			declared[crateName(key, spec)] = table
		}
	}
	add("dependencies", manifest.Dependencies)
	add("dev-dependencies", manifest.DevDependencies)
	add("build-dependencies", manifest.BuildDependencies)
	for target, t := range manifest.Target {
		add("target."+target+".dependencies", t.Dependencies)
		add("target."+target+".dev-dependencies", t.DevDependencies)
		add("target."+target+".build-dependencies", t.BuildDependencies)
	}
	if manifest.Workspace != nil {
		add("workspace.dependencies", manifest.Workspace.Dependencies)
	}
	for name, table := range declared {
		if !locked[name] {
			findings = append(findings, Finding{name, fmt.Sprintf("Cargo.toml declares it under [%s], and Cargo.lock does not lock it", table)})
		}
	}

	for _, p := range lock.Package {
		if p.Source == "" {
			continue
		}
		label := p.Name + "@" + p.Version
		git := strings.HasPrefix(p.Source, "git+")
		source := p.Source
		if git {
			source = "git"
		}
		if !allowed(sources, source) {
			findings = append(findings, Finding{label, fmt.Sprintf("it comes from %s, which is not an allowed source", p.Source)})
		}
		switch {
		case git && !gitCommitPinned(p.Source):
			findings = append(findings, Finding{label, "it is a git dependency not pinned to a commit"})
		case !git && p.Checksum == "":
			findings = append(findings, Finding{label, "it has no checksum, so what is built is not checked against what was locked"})
		}
	}
	return sorted(findings), nil
}

// crateName is the crate a dependency key names: the key, or the table's
// package when the key renames it.
func crateName(key string, spec any) string {
	if table, ok := spec.(map[string]any); ok {
		if pkg, ok := table["package"].(string); ok && pkg != "" {
			return pkg
		}
	}
	return key
}
