// Tests for named secrets in the credential file.
package credential_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// projectText is every file under dir, concatenated, for a test that proves a
// value never reaches disk in the clear.
func projectText(t *testing.T, dir string) string {
	t.Helper()
	var raw strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p) // #nosec G304 -- this test's own directory
			raw.Write(b)
		}
		return nil
	})
	return raw.String()
}

// TestSecrets_SaveListLookupRemove: a secret's fields are stored encrypted
// (never in the clear), listed by name without values, looked up one field at
// a time, replaced field by field, and removed by field or whole.
func TestSecrets_SaveListLookupRemove(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, field, value string }{
		{"dmvpn", "psk", "first-psk-value"},
		{"dmvpn", "psk", "dmvpn-psk-value"},
		{"dmvpn", "nhrp_key", "nhrp-value-1"},
		{"ca", "passphrase", "ca-passphrase-value"},
	} {
		if err := credential.SaveSecretField(dir, key, f.name, f.field, f.value); err != nil {
			t.Fatal(err)
		}
	}
	raw := projectText(t, dir)
	for _, v := range []string{"dmvpn-psk-value", "nhrp-value-1", "ca-passphrase-value", "first-psk-value"} {
		if strings.Contains(raw, v) {
			t.Fatalf("%q is in the project's files in the clear", v)
		}
	}

	list, err := credential.ListSecrets(dir)
	if err != nil || len(list) != 2 || list[0].Name != "ca" || list[1].Name != "dmvpn" ||
		strings.Join(list[1].Fields, ",") != "nhrp_key,psk" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	store := credential.NewLazyFileStore(dir).(credential.SecretStore)
	got, ok, err := store.LookupSecret(context.Background(), "dmvpn", "psk")
	if err != nil || !ok || got != "dmvpn-psk-value" {
		t.Fatalf("Lookup = %q, %v, %v (want the replaced value)", got, ok, err)
	}
	if _, ok, err := store.LookupSecret(context.Background(), "dmvpn", "absent"); ok || err != nil {
		t.Fatalf("an absent field answered %v, %v", ok, err)
	}
	if _, ok, err := store.LookupSecret(context.Background(), "nobody", "psk"); ok || err != nil {
		t.Fatalf("an absent secret answered %v, %v", ok, err)
	}

	if removed, err := credential.RemoveSecret(dir, "dmvpn", "psk"); err != nil || !removed {
		t.Fatalf("remove field = %v, %v", removed, err)
	}
	if _, ok, _ := store.LookupSecret(context.Background(), "dmvpn", "psk"); ok {
		t.Fatal("a removed field still answers")
	}
	if removed, err := credential.RemoveSecret(dir, "dmvpn", "nhrp_key"); err != nil || !removed {
		t.Fatalf("remove last field = %v, %v", removed, err)
	}
	if removed, err := credential.RemoveSecret(dir, "ca", ""); err != nil || !removed {
		t.Fatalf("remove whole = %v, %v", removed, err)
	}
	if removed, err := credential.RemoveSecret(dir, "ca", ""); err != nil || removed {
		t.Fatalf("a second removal = %v, %v", removed, err)
	}
	if list, _ := credential.ListSecrets(dir); len(list) != 0 {
		t.Fatalf("secrets left after removing every field: %+v", list)
	}
}

// TestSecrets_ValuesAndGenerated: every stored value comes back for a run to
// mask, a project with none needs no master key, and a generated value is
// letters and digits of the length asked, within its bounds.
func TestSecrets_ValuesAndGenerated(t *testing.T) {
	dir := t.TempDir()
	if values, err := credential.SecretValues(dir); err != nil || values != nil {
		t.Fatalf("an empty project: %v, %v", values, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pleiades", "master.key")); !os.IsNotExist(err) {
		t.Fatalf("an empty project made a master key: %v", err)
	}
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := credential.GenerateSecret(32)
	if err != nil || len(generated) != 32 || strings.Trim(generated, "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789") != "" {
		t.Fatalf("GenerateSecret(32) = %q, %v", generated, err)
	}
	for _, bad := range []int{0, credential.MinSecretLength - 1, credential.MaxGeneratedSecretLength + 1} {
		if _, err := credential.GenerateSecret(bad); err == nil {
			t.Errorf("GenerateSecret(%d) was not refused", bad)
		}
	}
	for field, value := range map[string]string{"psk": generated, "nhrp": "Nhrp-Value-77"} {
		if err := credential.SaveSecretField(dir, key, "dmvpn", field, value); err != nil {
			t.Fatal(err)
		}
	}
	values, err := credential.SecretValues(dir)
	if err != nil || len(values) != 2 || !slices.Contains(values, generated) || !slices.Contains(values, "Nhrp-Value-77") {
		t.Fatalf("SecretValues = %d values, %v", len(values), err)
	}
}

// TestSecrets_RefusesNamesAReferenceCannotName: a name or field that is not a
// lowercase identifier, and an empty value, are refused at the store, so no
// stored secret is one a runbook could not refer to.
func TestSecrets_RefusesNamesAReferenceCannotName(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ name, field, value string }{
		"an upper case name":   {"DMVPN", "psk", "v"},
		"a dotted name":        {"a.b", "psk", "v"},
		"a hyphen":             {"dm-vpn", "psk", "v"},
		"a leading digit":      {"1st", "psk", "v"},
		"a brace in the field": {"dmvpn", "psk}}", "v"},
		"an empty field":       {"dmvpn", "", "v"},
		"an empty value":       {"dmvpn", "psk", ""},
		"a short value":        {"dmvpn", "psk", "cisco12"},
		"a name too long":      {strings.Repeat("a", 65), "psk", "v"},
	} {
		if err := credential.SaveSecretField(dir, key, c.name, c.field, c.value); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	store := credential.NewLazyFileStore(dir).(credential.SecretStore)
	if _, _, err := store.LookupSecret(context.Background(), "Bad.Name", "psk"); err == nil {
		t.Error("a lookup by a name no reference can hold was not refused")
	}
}
