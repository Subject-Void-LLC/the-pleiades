// Package netconf: the acceptance test of the protocol-neutral yang.* rule
// (owner decision 2026-11-15), against one router read over both protocols:
// a Cisco Catalyst 8000V (IOS XE 17.15.1a) whose net.netconf.facts output
// (testdata/c8k-17.15.1a-netconf-facts.json, 2026-11-15) was taken minutes
// after restconftest's capture of its RESTCONF YANG library.
package netconf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// netconfFacts is the router's net.netconf.facts output under the rule
// before this one.
type netconfFacts struct {
	Count   int      `json:"yang.module_count"`
	Digest  string   `json:"yang.modules_digest"`
	Modules []string `json:"yang.modules"`
}

// digestOfAll is the digest the rule before this one recorded: SHA-256 (hex)
// over every module, the protocols' own included, as JSON.
func digestOfAll(t *testing.T, modules []Module) string {
	t.Helper()
	raw, err := json.Marshal(modules)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// namesOf is modules as yang.modules writes them.
func namesOf(modules []Module) []string {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		if m.Revision == "" {
			out = append(out, m.Name)
		} else {
			out = append(out, m.Name+"@"+m.Revision)
		}
	}
	return out
}

func TestProtocolNeutral_OneRouterReadsAlikeOverBothProtocols(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "c8k-17.15.1a-netconf-facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded netconfFacts
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}

	// The NETCONF library capture holds the full entries the digest covers;
	// it is the same data the recorded facts were read from, since every
	// module and the old rule's digest over all of them agree.
	reply, err := readLibrary([]byte(fixture(t, "c8k-17.15.1a-yang-library-reply.xml.gz")))
	if err != nil {
		t.Fatal(err)
	}
	reply.Source = yanglib.SourceLibrary11
	kn, err := yanglib.Keep(reply, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	overNETCONF := Discovery{}.WithLibrary(kn)
	if !slices.Equal(namesOf(overNETCONF.Modules), recorded.Modules) || len(overNETCONF.Modules) != recorded.Count || recorded.Count != 559 {
		t.Fatalf("the NETCONF capture lists %d modules, the recorded facts %d (%d): not the same read", len(overNETCONF.Modules), len(recorded.Modules), recorded.Count)
	}
	if digest := digestOfAll(t, overNETCONF.Modules); digest != recorded.Digest {
		t.Fatalf("the old rule's digest of the capture is %s, the recorded %s", digest, recorded.Digest)
	}

	lib, err := yanglib.ParseJSON(restconftest.IOSXELibraryJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	kr, err := yanglib.Keep(lib, Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	overRESTCONF := Discovery{}.WithLibrary(kr)

	if n, r := len(overNETCONF.DataModules()), len(overRESTCONF.DataModules()); n != r || n != 556 {
		t.Errorf("yang.module_count is %d over NETCONF and %d over RESTCONF, want 556 both", n, r)
	}
	if overNETCONF.ModulesDigest() != overRESTCONF.ModulesDigest() {
		t.Errorf("yang.modules_digest is %s over NETCONF and %s over RESTCONF, want one", overNETCONF.ModulesDigest(), overRESTCONF.ModulesDigest())
	}
	if got := strings.Join(namesOf(overNETCONF.OwnModules()), ","); got != "ietf-netconf@2011-06-01,ietf-netconf-with-defaults@2011-06-01,tailf-netconf-extensions@2021-06-18" {
		t.Errorf("netconf.modules = %s, want the three", got)
	}
	if got := strings.Join(namesOf(overRESTCONF.OwnModules()), ","); got != "tailf-rest-query@2023-04-13" {
		t.Errorf("restconf.modules = %s, want tailf-rest-query", got)
	}
}
