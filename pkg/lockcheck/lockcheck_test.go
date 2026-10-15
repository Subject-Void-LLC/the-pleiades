// Tests for verifying lockfiles against their manifests, a finding per
// way a lockfile falls short, and a clean lockfile with none.
package lockcheck

import (
	"strings"
	"testing"
)

// problems is every finding as "package: problem".
func problems(findings []Finding) string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Package+": "+f.Problem)
	}
	return strings.Join(out, "\n")
}

const npmManifestOK = `{"dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"jest":"^29.0.0"},"optionalDependencies":{"fsevents":"^2.3.0"}}`

// npmLockOK is a version 3 lockfile matching npmManifestOK.
const npmLockOK = `{"lockfileVersion":3,"packages":{
 "":{"dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"jest":"^29.0.0"},"optionalDependencies":{"fsevents":"^2.3.0"}},
 "node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-AAAA"},
 "node_modules/jest":{"version":"29.7.0","resolved":"https://registry.npmjs.org/jest/-/jest-29.7.0.tgz","integrity":"sha512-BBBB","dev":true},
 "node_modules/jest/node_modules/inner":{"version":"1.0.0","inBundle":true},
 "packages/app":{"version":"0.1.0"},
 "node_modules/app":{"resolved":"packages/app","link":true}
}}`

// TestNPM_ACleanLockfileHasNoFindings, bundled entries, workspace links
// and an optional dependency not installed on this platform included.
func TestNPM_ACleanLockfileHasNoFindings(t *testing.T) {
	f, _ := Lookup("npm")
	findings, err := f.Verify([]byte(npmLockOK), []byte(npmManifestOK), nil)
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings = %s, err = %v", problems(findings), err)
	}
}

// TestNPM_FindsEachShortfall changes one thing per case.
func TestNPM_FindsEachShortfall(t *testing.T) {
	f, _ := Lookup("npm")
	entry := `"node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-AAAA"}`
	for name, tc := range map[string]struct {
		lock, manifest string
		sources        []string
		want           string
	}{
		"not locked":         {npmLockOK, `{"dependencies":{"left-pad":"^1.3.0","lodash":"^4"},"devDependencies":{"jest":"^29.0.0"},"optionalDependencies":{"fsevents":"^2.3.0"}}`, nil, "lodash: package.json declares it in dependencies, and the lockfile was not made with it"},
		"a range change":     {npmLockOK, strings.Replace(npmManifestOK, "^1.3.0", "^2.0.0", 1), nil, "left-pad: package.json asks for ^2.0.0, and the lockfile was made for ^1.3.0"},
		"no longer declared": {npmLockOK, `{"dependencies":{"left-pad":"^1.3.0"},"optionalDependencies":{"fsevents":"^2.3.0"}}`, nil, "jest: the lockfile has it in devDependencies, and package.json no longer declares it"},
		"no integrity":       {strings.Replace(npmLockOK, `,"integrity":"sha512-AAAA"`, "", 1), npmManifestOK, nil, "left-pad@1.3.0: it has no integrity hash"},
		"sha1 only":          {strings.Replace(npmLockOK, "sha512-AAAA", "sha1-CCCC", 1), npmManifestOK, nil, "left-pad@1.3.0: its only integrity hash is sha1"},
		"plain http":         {strings.Replace(npmLockOK, "https://registry.npmjs.org/left-pad", "http://registry.npmjs.org/left-pad", 1), npmManifestOK, nil, "over http, not https"},
		"another host":       {strings.Replace(npmLockOK, "registry.npmjs.org/left-pad", "evil.example/left-pad", 1), npmManifestOK, nil, "left-pad@1.3.0: it comes from evil.example, which is not an allowed source"},
		"git not allowed":    {strings.Replace(npmLockOK, entry, `"node_modules/left-pad":{"version":"1.3.0","resolved":"git+ssh://git@github.com/x/left-pad.git#0123456789abcdef0123456789abcdef01234567"}`, 1), npmManifestOK, nil, "it comes from git, which is not an allowed source"},
		"git unpinned":       {strings.Replace(npmLockOK, entry, `"node_modules/left-pad":{"version":"1.3.0","resolved":"git+ssh://git@github.com/x/left-pad.git#main"}`, 1), npmManifestOK, []string{"registry.npmjs.org", "git"}, "left-pad@1.3.0: it is a git dependency not pinned to a commit"},
		"not installed":      {strings.Replace(npmLockOK, entry+",", "", 1), npmManifestOK, nil, "left-pad: the lockfile has no entry to install it from"},
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := f.Verify([]byte(tc.lock), []byte(tc.manifest), tc.sources)
			if err != nil {
				t.Fatal(err)
			}
			if got := problems(findings); !strings.Contains(got, tc.want) {
				t.Errorf("findings =\n%s\nwant one saying %q", got, tc.want)
			}
		})
	}
	git := strings.Replace(npmLockOK, entry, `"node_modules/left-pad":{"version":"1.3.0","resolved":"git+ssh://git@github.com/x/left-pad.git#0123456789abcdef0123456789abcdef01234567"}`, 1)
	if findings, _ := f.Verify([]byte(git), []byte(npmManifestOK), []string{"registry.npmjs.org", "git"}); len(findings) != 0 {
		t.Errorf("an allowed, pinned git dependency = %s", problems(findings))
	}
}

// TestNPM_RefusesWhatItCannotRead.
func TestNPM_RefusesWhatItCannotRead(t *testing.T) {
	f, _ := Lookup("npm")
	for name, lock := range map[string]string{
		"version 1": `{"lockfileVersion":1,"dependencies":{}}`,
		"not JSON":  `{`,
		"no root":   `{"lockfileVersion":3,"packages":{}}`,
	} {
		if _, err := f.Verify([]byte(lock), []byte(npmManifestOK), nil); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

const cargoManifestOK = `[package]
name = "app"
version = "0.1.0"

[dependencies]
serde = { version = "1", features = ["derive"] }
tokio-renamed = { package = "tokio", version = "1" }
local = { path = "../local" }

[target.'cfg(unix)'.dependencies]
libc = "0.2"

[dev-dependencies]
proptest = "1"
`

const cargoLockOK = `version = 4

[[package]]
name = "app"
version = "0.1.0"
dependencies = ["serde", "tokio", "local", "libc"]

[[package]]
name = "local"
version = "0.1.0"

[[package]]
name = "serde"
version = "1.0.210"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "aaaa"

[[package]]
name = "tokio"
version = "1.40.0"
source = "sparse+https://index.crates.io/"
checksum = "bbbb"

[[package]]
name = "libc"
version = "0.2.159"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "cccc"

[[package]]
name = "proptest"
version = "1.5.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "dddd"
`

// TestCargo_ACleanLockfileHasNoFindings, a renamed crate, a path crate
// and a target-specific dependency included.
func TestCargo_ACleanLockfileHasNoFindings(t *testing.T) {
	f, _ := Lookup("cargo")
	findings, err := f.Verify([]byte(cargoLockOK), []byte(cargoManifestOK), nil)
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings = %s, err = %v", problems(findings), err)
	}
}

// TestCargo_FindsEachShortfall changes one thing per case.
func TestCargo_FindsEachShortfall(t *testing.T) {
	f, _ := Lookup("cargo")
	for name, tc := range map[string]struct {
		lock, manifest string
		sources        []string
		want           string
	}{
		"not locked":       {cargoLockOK, cargoManifestOK + "rand = \"0.8\"\n", nil, "rand: Cargo.toml declares it under [dev-dependencies], and Cargo.lock does not lock it"},
		"no checksum":      {strings.Replace(cargoLockOK, "checksum = \"aaaa\"\n", "", 1), cargoManifestOK, nil, "serde@1.0.210: it has no checksum"},
		"another registry": {strings.Replace(cargoLockOK, "sparse+https://index.crates.io/", "sparse+https://crates.evil.example/", 1), cargoManifestOK, nil, "tokio@1.40.0: it comes from sparse+https://crates.evil.example/, which is not an allowed source"},
		"git unpinned":     {strings.Replace(cargoLockOK, "source = \"sparse+https://index.crates.io/\"\nchecksum = \"bbbb\"", "source = \"git+https://github.com/tokio-rs/tokio?branch=master#main\"", 1), cargoManifestOK, []string{cratesIOGit, "git"}, "tokio@1.40.0: it is a git dependency not pinned to a commit"},
		"git not allowed":  {strings.Replace(cargoLockOK, "source = \"sparse+https://index.crates.io/\"\nchecksum = \"bbbb\"", "source = \"git+https://github.com/tokio-rs/tokio#0123456789abcdef0123456789abcdef01234567\"", 1), cargoManifestOK, nil, "which is not an allowed source"},
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := f.Verify([]byte(tc.lock), []byte(tc.manifest), tc.sources)
			if err != nil {
				t.Fatal(err)
			}
			if got := problems(findings); !strings.Contains(got, tc.want) {
				t.Errorf("findings =\n%s\nwant one saying %q", got, tc.want)
			}
		})
	}
}

// TestCargo_RefusesWhatItCannotRead.
func TestCargo_RefusesWhatItCannotRead(t *testing.T) {
	f, _ := Lookup("cargo")
	for name, lock := range map[string]string{
		"version 2 or older": "[[package]]\nname = \"a\"\n",
		"not TOML":           "version = \n",
	} {
		if _, err := f.Verify([]byte(lock), []byte(cargoManifestOK), nil); err == nil {
			t.Errorf("%s was read", name)
		}
	}
	if _, err := f.Verify([]byte(cargoLockOK), []byte("[dependencies\n"), nil); err == nil {
		t.Error("a manifest that is not TOML was read")
	}
}

// TestRegistry finds each format by name and by its lockfile's name, and
// refuses a second registration of a name.
func TestRegistry(t *testing.T) {
	if strings.Join(Names(), ",") != "cargo,npm" {
		t.Errorf("Names = %v", Names())
	}
	if f, ok := ForLockFile("/srv/app/package-lock.json"); !ok || f.Name() != "npm" {
		t.Error("package-lock.json was not npm's")
	}
	if f, ok := ForLockFile("Cargo.lock"); !ok || f.Name() != "cargo" {
		t.Error("Cargo.lock was not cargo's")
	}
	if _, ok := ForLockFile("yarn.lock"); ok {
		t.Error("yarn.lock was claimed")
	}
	defer func() {
		if recover() == nil {
			t.Error("a second npm registered")
		}
	}()
	Register(npmFormat{})
}

// FuzzVerify: neither format panics on any input.
func FuzzVerify(f *testing.F) {
	f.Add(npmLockOK, npmManifestOK, "npm")
	f.Add(cargoLockOK, cargoManifestOK, "cargo")
	f.Fuzz(func(t *testing.T, lock, manifest, format string) {
		if fm, ok := Lookup(format); ok {
			_, _ = fm.Verify([]byte(lock), []byte(manifest), nil)
		}
	})
}
