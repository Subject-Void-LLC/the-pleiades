// Tests for set-host, and for add-host's refusal of what a device type
// refuses, driven through the real commands against a real project
// directory and read back through the same repository run loads from.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// windowsProject returns a project holding one windows_server, w, on
// the HTTPS port.
func windowsProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := runAddHost([]string{"w", "--dir", dir, "--type", "windows_server", "--set", "host=h", "--set", "port=5986"}); err != nil {
		t.Fatalf("add-host: %v", err)
	}
	return dir
}

// loadHost reads name back through the file repository, history and all.
func loadHost(t *testing.T, dir, name string) inventory.InventoryItem {
	t.Helper()
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("GetByName(%s): %v", name, err)
	}
	return item
}

// authorityPEM returns a self-signed CA certificate as PEM.
func authorityPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// TestSetHost_PinsAnAuthorityAndRecordsIt is the rotation an operator
// makes after re-issuing a host's certificate authority: the pin lands
// on the device, the device reads it as its TLS settings, and the change
// is a revision naming the property.
func TestSetHost_PinsAnAuthorityAndRecordsIt(t *testing.T) {
	dir := windowsProject(t)
	ca := authorityPEM(t)
	if err := runSetHost([]string{"w", "--dir", dir, "--set", "tls_ca_pem=" + ca}); err != nil {
		t.Fatalf("set-host: %v", err)
	}
	item := loadHost(t, dir, "w")
	if settings := devicetls.For(item); string(settings.CAPEM()) != ca {
		t.Errorf("the device's pinned authority is %q, want the one set", settings.CAPEM())
	}
	history := item.History()
	if item.Version() != 1 || len(history) != 1 || history[0].Field != devicetls.CAPEMProperty || history[0].NewValue != ca {
		t.Errorf("version %d, history %+v; want one revision setting %s", item.Version(), history, devicetls.CAPEMProperty)
	}

	// The same value again is not a change.
	if err := runSetHost([]string{"w", "--dir", dir, "--set", "tls_ca_pem=" + ca}); err != nil {
		t.Fatalf("set-host with the stored value: %v", err)
	}
	if v := loadHost(t, dir, "w").Version(); v != 1 {
		t.Errorf("an unchanged value moved the version to %d", v)
	}

	// Unset removes it, as a revision of its own.
	if err := runSetHost([]string{"w", "--dir", dir, "--unset", "tls_ca_pem"}); err != nil {
		t.Fatalf("set-host --unset: %v", err)
	}
	item = loadHost(t, dir, "w")
	if devicetls.For(item).PinnedCA() || item.Version() != 2 {
		t.Errorf("after unset: pinned %v, version %d", devicetls.For(item).PinnedCA(), item.Version())
	}
}

// TestSetHost_RefusesWhatTheTypeRefuses proves the device is rebuilt as
// its type before the write: a pin moved onto the HTTP port is refused,
// and the stored host is exactly as it was.
func TestSetHost_RefusesWhatTheTypeRefuses(t *testing.T) {
	dir := windowsProject(t)
	err := runSetHost([]string{"w", "--dir", dir, "--set", "port=5985", "--set", "tls_ca_pem=" + authorityPEM(t)})
	if err == nil || !strings.Contains(err.Error(), "not changed") || !strings.Contains(err.Error(), "HTTP one") {
		t.Fatalf("err = %v, want the type's refusal", err)
	}
	item := loadHost(t, dir, "w")
	if port, _ := item.Properties().Int("port"); port != 5986 || item.Version() != 0 || devicetls.For(item).PinnedCA() {
		t.Errorf("a refused change was written: port %d, version %d", port, item.Version())
	}
}

func TestSetHost_Refusals(t *testing.T) {
	dir := windowsProject(t)
	for name, args := range map[string][]string{
		"nothing to change":        {"w", "--dir", dir},
		"both set and unset":       {"w", "--dir", dir, "--set", "port=1", "--unset", "port"},
		"a reserved property":      {"w", "--dir", dir, "--unset", inventory.DiscoveredProperty},
		"a missing property":       {"w", "--dir", dir, "--unset", "nothere"},
		"a host that is not one":   {"nobody", "--dir", dir, "--set", "port=1"},
		"no name":                  {"--dir", dir, "--set", "port=1"},
		"both tagged and untagged": {"w", "--dir", dir, "--tag", "core", "--untag", "core"},
		"an empty tag":             {"w", "--dir", dir, "--tag", "core,"},
	} {
		if err := runSetHost(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if v := loadHost(t, dir, "w").Version(); v != 0 {
		t.Errorf("a refused call moved the version to %d", v)
	}
}

// TestSetHost_TagsAndUntagsAndRecordsIt proves --tag and --untag reach
// hosts.yaml, read back through the repository run loads from, each change
// a revision under the tags field, and that a tag already held, or one
// to remove that is not held, changes nothing.
func TestSetHost_TagsAndUntagsAndRecordsIt(t *testing.T) {
	dir := windowsProject(t)
	if err := runSetHost([]string{"w", "--dir", dir, "--tag", "core,lab", "--tag", "edge"}); err != nil {
		t.Fatalf("set-host --tag: %v", err)
	}
	item := loadHost(t, dir, "w")
	if got := fmt.Sprint(item.Tags()); got != "[core lab edge]" || item.Version() != 1 {
		t.Fatalf("tags %s at version %d, want [core lab edge] at 1", got, item.Version())
	}
	if h := item.History(); len(h) != 1 || h[0].Field != record.RevisionFieldTags {
		t.Errorf("history %+v, want one revision under %s", h, record.RevisionFieldTags)
	}

	if err := runSetHost([]string{"w", "--dir", dir, "--tag", "core", "--untag", "gone"}); err != nil {
		t.Fatalf("set-host with nothing new: %v", err)
	}
	if v := loadHost(t, dir, "w").Version(); v != 1 {
		t.Errorf("a tag already held moved the version to %d", v)
	}

	if err := runSetHost([]string{"w", "--dir", dir, "--untag", "lab,core", "--set", "port=5986"}); err != nil {
		t.Fatalf("set-host --untag: %v", err)
	}
	hosts, err := inv.ReadHosts(filepath.Join(dir, inv.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || fmt.Sprint(hosts[0].Tags) != "[edge]" {
		t.Errorf("hosts.yaml holds %+v, want the one host tagged [edge]", hosts)
	}

	if err := runSetHost([]string{"w", "--dir", dir, "--untag", "edge"}); err != nil {
		t.Fatalf("set-host --untag the last tag: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, inv.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tags:") {
		t.Errorf("a host with no tags left still writes a tags key:\n%s", data)
	}
}

// TestSetHost_AnUnknownFlagIsNamed is the report an agent got wrong: a flag
// set-host does not define took the host name as its value, and the
// command said the name was missing instead of naming the flag.
func TestSetHost_AnUnknownFlagIsNamed(t *testing.T) {
	dir := windowsProject(t)
	for _, args := range [][]string{
		{"--dir", dir, "--label", "w"},
		{"w", "--dir", dir, "--label", "core"},
	} {
		err := runSetHost(args)
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -label") {
			t.Errorf("set-host %v: err = %v, want the unknown flag named", args, err)
		}
	}
}

// TestAddHost_RefusesWhatTheTypeRefuses proves add-host builds the
// device before writing it, so a refused property never reaches the
// inventory.
func TestAddHost_RefusesWhatTheTypeRefuses(t *testing.T) {
	dir := windowsProject(t)
	err := runAddHost([]string{"x", "--dir", dir, "--type", "windows_server", "--set", "host=h", "--set", "tls_ca_pem=" + authorityPEM(t)})
	if err == nil || !strings.Contains(err.Error(), "not added") {
		t.Fatalf("err = %v, want the type's refusal", err)
	}
	hosts, err := inv.ReadHosts(filepath.Join(dir, inv.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.Name == "x" {
			t.Error("a refused host was written")
		}
	}
}
