// Tests for confirm-tls-pin over a real project inventory: a confirmation is
// a comparison a person made, so only the pin recorded, written in either
// case, is confirmed, and only while it is the one the host trusts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// pinProject writes a project holding one generic_http host with props and,
// when firstUse is set, that pin recorded on first use, and returns its
// directory.
func pinProject(t *testing.T, props map[string]inventory.PropertyValue, firstUse string) string {
	t.Helper()
	dir := t.TempDir()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), factory)
	item, err := factory.Build(record.Record{ID: "h1", Name: "api1", Type: "generic_http", Properties: props, State: inventory.StateActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if firstUse != "" {
		stored, err := repo.GetByName(context.Background(), "api1")
		if err != nil {
			t.Fatal(err)
		}
		stored.(interface{ RecordFirstUsePin(string) }).RecordFirstUsePin(firstUse)
		if err := repo.Save(context.Background(), stored); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestConfirmTLSPin_ConfirmsOnlyTheRecordedPinItTrusts walks each refusal
// and then the confirmation itself.
func TestConfirmTLSPin_ConfirmsOnlyTheRecordedPinItTrusts(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	base := map[string]inventory.PropertyValue{"base_url": "https://10.0.0.9"}
	for name, tc := range map[string]struct {
		dir  string
		args []string
		want string
	}{
		"not a pin":             {pinProject(t, base, pin), []string{"api1", "trust-me"}, "a pin is"},
		"one argument":          {pinProject(t, base, pin), []string{"api1"}, confirmTLSPinUsage},
		"no such host":          {pinProject(t, base, pin), []string{"api2", pin}, "api2"},
		"no first-use pin":      {pinProject(t, base, ""), []string{"api1", pin}, "holds no pin recorded on first use"},
		"another pin":           {pinProject(t, base, pin), []string{"api1", "sha256:" + strings.Repeat("cd", 32)}, "compare the recorded one"},
		"the key form":          {pinProject(t, base, pin), []string{"api1", "spki-sha256:" + strings.Repeat("ab", 32)}, "compare the recorded one"},
		"overridden by its own": {pinProject(t, map[string]inventory.PropertyValue{"base_url": "https://10.0.0.9", devicetls.FingerprintProperty: "sha256:" + strings.Repeat("ef", 32)}, pin), []string{"api1", pin}, "override"},
	} {
		if err := runConfirmTLSPin(append(tc.args, "--dir", tc.dir)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal saying %q", name, err, tc.want)
		}
	}

	dir := pinProject(t, base, pin)
	out := captureStdout(t, func() {
		if err := runConfirmTLSPin([]string{"api1", strings.ToUpper(pin), "--dir", dir}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "confirmed api1's pin "+pin) {
		t.Errorf("output %q", out)
	}
	item, err := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory()).GetByName(context.Background(), "api1")
	if err != nil {
		t.Fatal(err)
	}
	if s := devicetls.For(item); len(s.Pins()) != 1 || s.Pins()[0] != pin || s.FirstUsePin() != "" {
		t.Errorf("after confirming: pins %q, first use %q", s.Pins(), s.FirstUsePin())
	}
}

// TestConfirmTLSPin_JSONIsTheSameModel: --json prints the confirmation as
// one document holding the pin, where it now is, and the pins the stored
// host trusts, and nothing else on stdout.
func TestConfirmTLSPin_JSONIsTheSameModel(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	dir := pinProject(t, map[string]inventory.PropertyValue{"base_url": "https://10.0.0.9"}, pin)
	out := captureStdout(t, func() {
		if err := runConfirmTLSPin([]string{"api1", pin, "--dir", dir, "--json"}); err != nil {
			t.Error(err)
		}
	})
	var got confirmedPin
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not one confirmation document: %v\n%s", err, out)
	}
	if dec.More() {
		t.Errorf("stdout holds more than one document: %s", out)
	}
	if want := (confirmedPin{Host: "api1", Pin: pin, Property: devicetls.FingerprintProperty, Pins: []string{pin}}); !reflect.DeepEqual(got, want) {
		t.Errorf("--json = %+v, want %+v", got, want)
	}
}

// TestSetHost_APersonsPinDropsTheFirstUsePin: set-host writing tls_fingerprint
// drops the pin recorded on first use, so unsetting tls_fingerprint later
// leaves the host trusting nothing, never the unchecked pin again.
func TestSetHost_APersonsPinDropsTheFirstUsePin(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	dir := pinProject(t, map[string]inventory.PropertyValue{"base_url": "https://10.0.0.9"}, pin)
	stored := func() devicetls.Settings {
		t.Helper()
		item, err := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory()).GetByName(context.Background(), "api1")
		if err != nil {
			t.Fatal(err)
		}
		return devicetls.For(item)
	}
	person := "sha256:" + strings.Repeat("cd", 32)
	captureStdout(t, func() {
		if err := runSetHost([]string{"api1", "--set", "tls_fingerprint=" + person, "--dir", dir}); err != nil {
			t.Error(err)
		}
	})
	if s := stored(); s.UnusedFirstUsePin() != "" || len(s.Pins()) != 1 || s.Pins()[0] != person {
		t.Fatalf("after set-host: pins %q, unused first use %q", s.Pins(), s.UnusedFirstUsePin())
	}
	captureStdout(t, func() {
		if err := runSetHost([]string{"api1", "--unset", "tls_fingerprint", "--dir", dir}); err != nil {
			t.Error(err)
		}
	})
	if s := stored(); s.Pins() != nil || s.FirstUsePin() != "" {
		t.Errorf("after unsetting tls_fingerprint the host trusts %q (first use %q), want nothing", s.Pins(), s.FirstUsePin())
	}
}

// TestPrintOnboard_ListsAFirstUsePinForReview: the pin an onboarding
// recorded is printed whole, both forms and the names, with the command that
// confirms it, every device-supplied field escaped.
func TestPrintOnboard_ListsAFirstUsePinForReview(t *testing.T) {
	var b bytes.Buffer
	printOnboard(&b, onboard.Result{Device: "api1", Type: "generic_http", Protocol: "http", State: "active", Changed: true, FirstUse: &onboard.FirstUsePin{
		Fingerprint: "sha256:aa", KeyFingerprint: "spki-sha256:bb", Subject: "CN=dev\x1b[31m", Issuer: "CN=dev", NotAfter: "2030-01-01", Untrusted: "unknown authority",
	}})
	out := b.String()
	for _, want := range []string{"pinned on first use, unconfirmed: sha256:aa", "key: spki-sha256:bb", "valid until: 2030-01-01", "pleiades confirm-tls-pin api1 sha256:aa"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not say %q", out, want)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("output %q holds a terminal escape", out)
	}
}
