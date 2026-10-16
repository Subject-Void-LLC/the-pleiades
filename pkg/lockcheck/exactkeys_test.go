// Tests that a lockfile and its manifest are read by their exact key
// names, as npm and Cargo read them, so a key differing only in case can
// neither hide nor replace the one the package manager uses.
package lockcheck

import (
	"strings"
	"testing"
)

// TestNPM_ReadsKeysAsNpmDoes: encoding/json matches a key to a field
// regardless of case, and npm (JavaScript) does not, so each of these
// would have read differently from what npm installs.
func TestNPM_ReadsKeysAsNpmDoes(t *testing.T) {
	f, _ := Lookup("npm")
	for name, tc := range map[string]struct {
		lock, manifest string
		want           string // a finding to contain; "" for none
	}{
		"a second Resolved does not replace resolved": {
			strings.Replace(npmLockOK, `"resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"`,
				`"resolved":"https://evil.example/left-pad-1.3.0.tgz","Resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"`, 1),
			npmManifestOK, "left-pad@1.3.0: it comes from evil.example, which is not an allowed source",
		},
		"Link is not link": {
			strings.Replace(npmLockOK, `"node_modules/app":{"resolved":"packages/app","link":true}`, `"node_modules/app":{"version":"0.1.0","Link":true}`, 1),
			npmManifestOK, "app@0.1.0: it has no resolved source",
		},
		"a later Packages does not override packages": {
			strings.Replace(strings.Replace(npmLockOK, `"resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"`, `"resolved":"https://evil.example/left-pad-1.3.0.tgz"`, 1),
				"\n}}", "\n},"+`"Packages":{"node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-AAAA"}}}`, 1),
			npmManifestOK, "left-pad@1.3.0: it comes from evil.example, which is not an allowed source",
		},
		"a later Dependencies does not override dependencies": {
			npmLockOK, strings.Replace(npmManifestOK, `{"dependencies":{"left-pad":"^1.3.0"}`, `{"dependencies":{"left-pad":"^2.0.0"},"Dependencies":{"left-pad":"^1.3.0"}`, 1),
			"left-pad: package.json asks for ^2.0.0, and the lockfile was made for ^1.3.0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := f.Verify([]byte(tc.lock), []byte(tc.manifest), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := problems(findings)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Errorf("findings =\n%s\nwant one saying %q", got, tc.want)
			}
		})
	}
}

// TestCargo_ReadsKeysAsCargoDoes: go-toml falls back to a field matched
// regardless of case, and Cargo's keys are exact, so a SOURCE beside the
// source Cargo fetches from would have replaced it.
func TestCargo_ReadsKeysAsCargoDoes(t *testing.T) {
	f, _ := Lookup("cargo")
	for name, tc := range map[string]struct {
		lock, manifest string
		want           string
	}{
		"a SOURCE does not replace source": {
			strings.Replace(cargoLockOK, "source = \"sparse+https://index.crates.io/\"\n",
				"source = \"git+https://evil.example/tokio#main\"\nSOURCE = \"sparse+https://index.crates.io/\"\n", 1),
			cargoManifestOK, "tokio@1.40.0: it comes from git+https://evil.example/tokio#main, which is not an allowed source",
		},
		"a PACKAGE does not lock what package does not": {
			cargoLockOK + "\n[[PACKAGE]]\nname = \"rand\"\nversion = \"0.8.5\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"eeee\"\n",
			strings.Replace(cargoManifestOK, "[dependencies]\n", "[dependencies]\nrand = \"0.8\"\n", 1),
			"rand: Cargo.toml declares it under [dependencies], and Cargo.lock does not lock it",
		},
	} {
		t.Run(name, func(t *testing.T) {
			findings, err := f.Verify([]byte(tc.lock), []byte(tc.manifest), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := problems(findings); !strings.Contains(got, tc.want) {
				t.Errorf("findings =\n%s\nwant one saying %q", got, tc.want)
			}
		})
	}
}
