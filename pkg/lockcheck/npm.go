// npm's package-lock.json (lockfile versions 2 and 3) against package.json.
package lockcheck

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// npmFormat is the npm plug-in.
type npmFormat struct{}

func (npmFormat) Name() string             { return "npm" }
func (npmFormat) LockFile() string         { return "package-lock.json" }
func (npmFormat) ManifestFile() string     { return "package.json" }
func (npmFormat) DefaultSources() []string { return []string{"registry.npmjs.org"} }

// npmLock is the part of package-lock.json read here.
type npmLock struct {
	LockfileVersion int                 `json:"lockfileVersion"`
	Packages        map[string]npmEntry `json:"packages"`
}

// npmEntry is one entry under packages.
type npmEntry struct {
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Integrity            string            `json:"integrity"`
	Link                 bool              `json:"link"`
	InBundle             bool              `json:"inBundle"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// npmManifest is the part of package.json read here.
type npmManifest struct {
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// Verify checks a package-lock.json against its package.json.
//
// Sources are registry host names (registry.npmjs.org), "git" for a git
// dependency and "file" for a local tarball or directory; a bundled entry
// (inBundle) and a workspace link carry their parent's integrity and are
// not separate sources.
func (f npmFormat) Verify(lockData, manifestData []byte, sources []string) ([]Finding, error) {
	var lock npmLock
	if err := json.Unmarshal(lockData, &lock); err != nil {
		return nil, fmt.Errorf("package-lock.json is not valid JSON: %w", err)
	}
	if lock.LockfileVersion != 2 && lock.LockfileVersion != 3 {
		return nil, fmt.Errorf("package-lock.json is lockfile version %d; this reads versions 2 and 3 (npm 7 and later write them)", lock.LockfileVersion)
	}
	var manifest npmManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("package.json is not valid JSON: %w", err)
	}
	if len(sources) == 0 {
		sources = f.DefaultSources()
	}
	root, ok := lock.Packages[""]
	if !ok {
		return nil, fmt.Errorf("package-lock.json has no root entry (packages[\"\"])")
	}

	var findings []Finding
	for _, group := range []struct {
		kind           string
		declared, lock map[string]string
		optional       bool
	}{
		{"dependencies", manifest.Dependencies, root.Dependencies, false},
		{"devDependencies", manifest.DevDependencies, root.DevDependencies, false},
		{"optionalDependencies", manifest.OptionalDependencies, root.OptionalDependencies, true},
	} {
		for name, spec := range group.declared {
			locked, ok := group.lock[name]
			switch {
			case !ok:
				findings = append(findings, Finding{name, fmt.Sprintf("package.json declares it in %s, and the lockfile was not made with it", group.kind)})
			case locked != spec:
				findings = append(findings, Finding{name, fmt.Sprintf("package.json asks for %s, and the lockfile was made for %s", spec, locked)})
			case !group.optional:
				if _, installed := lock.Packages["node_modules/"+name]; !installed {
					findings = append(findings, Finding{name, "the lockfile has no entry to install it from"})
				}
			}
		}
		for name := range group.lock {
			if _, ok := group.declared[name]; !ok {
				findings = append(findings, Finding{name, fmt.Sprintf("the lockfile has it in %s, and package.json no longer declares it", group.kind)})
			}
		}
	}

	for key, e := range lock.Packages {
		if key == "" || e.Link || e.InBundle || !strings.Contains(key, "node_modules/") {
			continue
		}
		name := key[strings.LastIndex(key, "node_modules/")+len("node_modules/"):]
		label := name
		if e.Version != "" {
			label += "@" + e.Version
		}
		findings = append(findings, npmEntryFindings(label, e, sources)...)
	}
	return sorted(findings), nil
}

// npmEntryFindings checks one installed entry's source and integrity.
func npmEntryFindings(label string, e npmEntry, sources []string) []Finding {
	var out []Finding
	source, problem := npmSource(e.Resolved)
	switch {
	case problem != "":
		out = append(out, Finding{label, problem})
	case !allowed(sources, source):
		out = append(out, Finding{label, fmt.Sprintf("it comes from %s, which is not an allowed source", source)})
	}
	switch {
	case e.Integrity == "" && source == "git":
		// A git dependency is pinned by its commit in resolved, and npm
		// records no integrity for one; an allowed git source is enough.
	case e.Integrity == "":
		out = append(out, Finding{label, "it has no integrity hash, so what is installed is not checked against what was locked"})
	case !strongIntegrity(e.Integrity):
		out = append(out, Finding{label, "its only integrity hash is sha1, which no longer resists a forged package"})
	}
	if source == "git" && !gitCommitPinned(e.Resolved) {
		out = append(out, Finding{label, "it is a git dependency not pinned to a commit"})
	}
	return out
}

// npmSource names where a resolved URL fetches from: a registry host,
// "git" or "file"; a problem when it is none of them or plain http.
func npmSource(resolved string) (string, string) {
	switch {
	case resolved == "":
		return "", "it has no resolved source"
	case strings.HasPrefix(resolved, "git+") || strings.HasPrefix(resolved, "git:") || strings.HasPrefix(resolved, "github:"):
		return "git", ""
	case strings.HasPrefix(resolved, "file:"):
		return "file", ""
	}
	u, err := url.Parse(resolved)
	if err != nil || u.Host == "" {
		return "", fmt.Sprintf("its resolved source %q is not a URL this reads", resolved)
	}
	if u.Scheme != "https" {
		return u.Host, fmt.Sprintf("it is fetched from %s over %s, not https", u.Host, u.Scheme)
	}
	return u.Host, ""
}

// strongIntegrity reports whether an SRI string holds a sha256, sha384 or
// sha512 hash.
func strongIntegrity(sri string) bool {
	for _, h := range strings.Fields(sri) {
		if strings.HasPrefix(h, "sha512-") || strings.HasPrefix(h, "sha384-") || strings.HasPrefix(h, "sha256-") {
			return true
		}
	}
	return false
}

// gitCommitPinned reports whether a git URL ends in a full commit hash.
func gitCommitPinned(resolved string) bool {
	_, ref, ok := strings.Cut(resolved, "#")
	if !ok || len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
