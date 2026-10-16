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

// cargoPackage is one [[package]] of Cargo.lock.
type cargoPackage struct {
	Name, Version, Source, Checksum string
}

// A Cargo.lock and a Cargo.toml are decoded into maps, whose keys go-toml
// keeps exactly as written, and read by their exact keys from there.
// Decoded into structs, go-toml falls back to matching a key to a field
// regardless of case, so a SOURCE beside the source Cargo fetches from
// would have replaced it, and a [[PACKAGE]] would have counted as locked;
// Cargo reads only the key spelled as it is.

// cargoPackages reads Cargo.lock's [[package]] array.
func cargoPackages(doc map[string]any) ([]cargoPackage, error) {
	raw, ok := doc["package"]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("Cargo.lock's package is not an array of tables")
	}
	out := make([]cargoPackage, 0, len(list))
	for i, item := range list {
		table, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Cargo.lock's package %d is not a table", i+1)
		}
		var p cargoPackage
		for key, field := range map[string]*string{"name": &p.Name, "version": &p.Version, "source": &p.Source, "checksum": &p.Checksum} {
			if v, present := table[key]; present {
				text, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("Cargo.lock's package %d has a %s that is not a string", i+1, key)
				}
				*field = text
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// subtable is doc[key] when it is a table, else nil.
func subtable(doc map[string]any, key string) map[string]any {
	table, _ := doc[key].(map[string]any)
	return table
}

// Verify checks a Cargo.lock against its Cargo.toml.
//
// Sources are Cargo's own source strings (registry+..., sparse+...), plus
// "git" for a git dependency. A local path crate has no source and is not
// checked as one. A workspace member's own manifest is checked by naming
// it, with the workspace's Cargo.lock, in its own call.
func (f cargoFormat) Verify(lockData, manifestData []byte, sources []string) ([]Finding, error) {
	var lock map[string]any
	if err := toml.Unmarshal(lockData, &lock); err != nil {
		return nil, fmt.Errorf("Cargo.lock is not valid TOML: %w", err)
	}
	version, _ := lock["version"].(int64)
	if version != 3 && version != 4 {
		return nil, fmt.Errorf("Cargo.lock is version %v; this reads versions 3 and 4 (Cargo 1.53 and later write them)", lock["version"])
	}
	packages, err := cargoPackages(lock)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err := toml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("Cargo.toml is not valid TOML: %w", err)
	}
	if len(sources) == 0 {
		sources = f.DefaultSources()
	}

	locked := map[string]bool{}
	for _, p := range packages {
		locked[p.Name] = true
	}
	var findings []Finding
	declared := map[string]string{}
	add := func(table string, deps map[string]any) {
		for key, spec := range deps {
			declared[crateName(key, spec)] = table
		}
	}
	for _, table := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		add(table, subtable(manifest, table))
	}
	for target, t := range subtable(manifest, "target") {
		spec, _ := t.(map[string]any)
		for _, table := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			add("target."+target+"."+table, subtable(spec, table))
		}
	}
	add("workspace.dependencies", subtable(subtable(manifest, "workspace"), "dependencies"))
	for name, table := range declared {
		if !locked[name] {
			findings = append(findings, Finding{name, fmt.Sprintf("Cargo.toml declares it under [%s], and Cargo.lock does not lock it", table)})
		}
	}

	for _, p := range packages {
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
