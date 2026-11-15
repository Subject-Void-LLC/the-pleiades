// Tests of the two families a stored credential holds: each save replaces
// its own family whole and copies the other as stored, never decrypting it,
// and every save and every read holds the credential to the same rules.
package credential_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"go.yaml.in/yaml/v3"
)

// v3User is an authPriv SNMPv3 user, alone.
func v3User() credential.Credential {
	return credential.Credential{
		SNMPUser:         "monitor",
		SNMPAuthProtocol: wire.SNMPAuthSHA256, SNMPAuthPassphrase: "auth-pass-phrase",
		SNMPPrivProtocol: wire.SNMPPrivAES256, SNMPPrivPassphrase: "priv-pass-phrase",
	}
}

// storedEntry reads device's entry from dir's credential file as plain YAML
// keys and values, the bytes a save wrote, decrypting nothing.
func storedEntry(t *testing.T, dir, device string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Devices map[string]map[string]any `yaml:"devices"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Devices[device]
}

// lookup reads device's credential from dir with key.
func lookup(t *testing.T, dir string, key []byte, device string) (credential.Credential, error) {
	t.Helper()
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	return store.Lookup(context.Background(), device)
}

// TestSaveFileStore_TheFamilyASaveLeavesIsCopiedNeverDecrypted: a login is
// stored under one key, then the SNMP family is saved under a different one.
// Had the save decrypted the login to write it again, the second key could
// not have opened it; instead the login's ciphertext is byte for byte what
// the first save wrote, and the same holds the other way round.
func TestSaveFileStore_TheFamilyASaveLeavesIsCopiedNeverDecrypted(t *testing.T) {
	dir := t.TempDir()
	login := credential.Credential{Username: "admin", Password: "a-real-password", EnablePassword: "en4ble-s3cret"}
	if err := credential.SaveFileStore(dir, testKey('a'), "r1", login); err != nil {
		t.Fatal(err)
	}
	before := storedEntry(t, dir, "r1")
	if err := credential.SaveFileStoreWith(dir, testKey('b'), "r1", v3User(), credential.SaveOptions{Families: credential.SNMPFamily}); err != nil {
		t.Fatalf("saving SNMP under another key: %v", err)
	}
	after := storedEntry(t, dir, "r1")
	for _, k := range []string{"username", "password_encrypted", "enable_password_encrypted"} {
		if before[k] == nil || after[k] != before[k] {
			t.Errorf("the login's %s changed when only SNMP was saved: %v to %v", k, before[k], after[k])
		}
	}
	if after["snmp_user"] != "monitor" || after["snmp_auth_passphrase_encrypted"] == nil {
		t.Errorf("the SNMP family was not written: %v", after)
	}

	snmpBefore := storedEntry(t, dir, "r1")
	if err := credential.SaveFileStore(dir, testKey('c'), "r1", credential.Credential{Username: "admin", Password: "another-password"}); err != nil {
		t.Fatalf("saving a login under a third key: %v", err)
	}
	snmpAfter := storedEntry(t, dir, "r1")
	for _, k := range []string{"snmp_user", "snmp_auth_protocol", "snmp_auth_passphrase_encrypted", "snmp_priv_protocol", "snmp_priv_passphrase_encrypted"} {
		if snmpBefore[k] == nil || snmpAfter[k] != snmpBefore[k] {
			t.Errorf("the SNMP %s changed when only a login was saved", k)
		}
	}
	if _, stale := snmpAfter["enable_password_encrypted"]; stale {
		t.Error("the new login kept the old login's enable secret")
	}
}

// TestSaveFileStore_EachFamilyIsReplacedWholeAndKeepsTheOther: switching
// from a v3 user to a community leaves no v3 key behind and keeps the login;
// a login saved after keeps the community; and naming the SNMP family with
// nothing in it clears SNMP while the login stays.
func TestSaveFileStore_EachFamilyIsReplacedWholeAndKeepsTheOther(t *testing.T) {
	dir := t.TempDir()
	key := testKey('a')
	withUser := v3User()
	withUser.Username, withUser.Password = "admin", "a-real-password"
	if err := credential.SaveFileStore(dir, key, "r1", withUser); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "r1", credential.Credential{SNMPCommunity: "c0mmunity-s3cret"}); err != nil {
		t.Fatalf("switching to a community: %v", err)
	}
	got, err := lookup(t, dir, key, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SNMPCommunity != "c0mmunity-s3cret" || got.SNMPUser != "" || got.SNMPAuthProtocol != "" || got.SNMPAuthPassphrase != "" || got.SNMPPrivPassphrase != "" {
		t.Errorf("after switching to a community the SNMP family is %v", got.SNMP())
	}
	if got.Username != "admin" || got.Password != "a-real-password" {
		t.Errorf("saving SNMP lost the login: %v", got)
	}

	if err := credential.SaveFileStore(dir, key, "r1", credential.Credential{Username: "admin", PrivateKeyPEM: []byte("key-bytes")}); err != nil {
		t.Fatal(err)
	}
	if got, _ = lookup(t, dir, key, "r1"); got.Password != "" || string(got.PrivateKeyPEM) != "key-bytes" || got.SNMPCommunity != "c0mmunity-s3cret" {
		t.Errorf("a login save should replace the login and keep the community: %v", got)
	}

	if err := credential.SaveFileStoreWith(dir, key, "r1", credential.Credential{}, credential.SaveOptions{Families: credential.SNMPFamily}); err != nil {
		t.Fatal(err)
	}
	if got, _ = lookup(t, dir, key, "r1"); !got.SNMP().Empty() || string(got.PrivateKeyPEM) != "key-bytes" {
		t.Errorf("clearing the SNMP family should keep the login and nothing else: %v", got)
	}
	if entry := storedEntry(t, dir, "r1"); entry["snmp_community_encrypted"] != nil {
		t.Error("the cleared community is still in the file")
	}
}

// TestSaveFileStore_RefusesWhatItWouldDropOrCouldNotUse: a field of a
// family the save does not replace, an SNMP family no agent accepts, and a
// private key in the certificate field are each refused before anything is
// written, and a refusal repeats no secret.
func TestSaveFileStore_RefusesWhatItWouldDropOrCouldNotUse(t *testing.T) {
	const keyPEM = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----\n"
	certPEM := "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ\n-----END CERTIFICATE-----\n"
	for _, tc := range []struct {
		name string
		cred credential.Credential
		opts credential.SaveOptions
		want string
	}{
		{"SNMP fields on a login save", credential.Credential{Username: "u", Password: "p", SNMPCommunity: "c0mmunity-s3cret"}, credential.SaveOptions{Families: credential.LoginFamily}, "replaces only the login"},
		{"login fields on an SNMP save", credential.Credential{Username: "u", SNMPCommunity: "c0mmunity-s3cret"}, credential.SaveOptions{Families: credential.SNMPFamily}, "replaces only the SNMP"},
		{"a short community allowed on a login save", credential.Credential{Username: "u", Password: "p"}, credential.SaveOptions{AllowShortCommunity: true}, "short community"},
		{"privacy without authentication", credential.Credential{SNMPUser: "u", SNMPPrivProtocol: wire.SNMPPrivDES, SNMPPrivPassphrase: "priv-pass-phrase"}, credential.SaveOptions{}, wire.SecretSNMPPrivProtocol},
		{"an unknown protocol", credential.Credential{SNMPUser: "u", SNMPAuthProtocol: "sha1", SNMPAuthPassphrase: "auth-pass-phrase"}, credential.SaveOptions{}, wire.SecretSNMPAuthProtocol},
		{"a community with a user", credential.Credential{SNMPCommunity: "c0mmunity-s3cret", SNMPUser: "u"}, credential.SaveOptions{}, wire.SecretSNMPCommunity},
		{"a short community", credential.Credential{SNMPCommunity: "public"}, credential.SaveOptions{}, wire.SecretSNMPCommunity},
		{"a key beside the certificate", credential.Credential{CertificatePEM: []byte(certPEM + keyPEM), PrivateKeyPEM: []byte(keyPEM)}, credential.SaveOptions{}, "private key field"},
	} {
		dir := t.TempDir()
		err := credential.SaveFileStoreWith(dir, testKey('a'), "r1", tc.cred, tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: SaveFileStoreWith = %v, want a refusal naming %q", tc.name, err, tc.want)
			continue
		}
		for _, secret := range []string{tc.cred.SNMPCommunity, tc.cred.SNMPAuthPassphrase, tc.cred.SNMPPrivPassphrase, "MIGHAgEAMBMGByqGSM49"} {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the refusal repeats a secret: %v", tc.name, err)
			}
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".pleiades", "credentials.yaml")); statErr == nil {
			t.Errorf("%s: a refused save wrote the credential file", tc.name)
		}
	}
}

// TestLookup_HoldsWhatWasStoredToTheSameRules: a protocol name edited in the
// file, a short community whose mark was removed, and a key a save before
// this check put beside a certificate are each refused on read, so none
// reaches a run.
func TestLookup_HoldsWhatWasStoredToTheSameRules(t *testing.T) {
	key := testKey('a')
	edit := func(t *testing.T, dir string, change func(entry map[string]any)) {
		t.Helper()
		path := filepath.Join(dir, ".pleiades", "credentials.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]map[string]map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		change(doc["devices"]["r1"])
		out, _ := yaml.Marshal(doc)
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	if err := credential.SaveFileStore(dir, key, "r1", v3User()); err != nil {
		t.Fatal(err)
	}
	edit(t, dir, func(e map[string]any) { e["snmp_auth_protocol"] = "rot13" })
	if _, err := lookup(t, dir, key, "r1"); err == nil || !strings.Contains(err.Error(), wire.SecretSNMPAuthProtocol) {
		t.Errorf("an edited protocol name was read as %v", err)
	}

	dir = t.TempDir()
	if err := credential.SaveFileStoreWith(dir, key, "r1", credential.Credential{SNMPCommunity: "public"}, credential.SaveOptions{AllowShortCommunity: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := lookup(t, dir, key, "r1"); err != nil || got.SNMPCommunity != "public" {
		t.Fatalf("an allowed short community read back as %v, %v", got, err)
	}
	edit(t, dir, func(e map[string]any) { delete(e, "short_community") })
	if _, err := lookup(t, dir, key, "r1"); err == nil || !strings.Contains(err.Error(), "add-credential r1") {
		t.Errorf("a short community without its mark was read as %v", err)
	}

	dir = t.TempDir()
	if err := credential.SaveFileStore(dir, key, "r1", credential.Credential{Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	svc, _ := crypto.NewAESService(key)
	sealed, _ := svc.Encrypt([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN EC PRIVATE KEY-----\nMHcC\n-----END EC PRIVATE KEY-----\n"))
	edit(t, dir, func(e map[string]any) { e["certificate_encrypted"] = base64.StdEncoding.EncodeToString(sealed) })
	if _, err := lookup(t, dir, key, "r1"); err == nil || !strings.Contains(err.Error(), "private key field") || strings.Contains(err.Error(), "MHcC") {
		t.Errorf("a key stored beside a certificate was read as %v", err)
	}
}

// TestCredential_WithSNMPReplacesOnlyTheSNMPFamily: WithSNMP sets all six
// SNMP fields from its argument, clearing any it leaves empty, and the login
// is untouched; HasLogin sees a login field and no SNMP one; and the family
// names read as a message says them.
func TestCredential_WithSNMPReplacesOnlyTheSNMPFamily(t *testing.T) {
	login := credential.Credential{Username: "admin", Token: "a-token-value"}.WithSNMP(v3User().SNMP())
	got := login.WithSNMP(wire.SNMPCredential{Community: "c0mmunity-s3cret"})
	if got.Username != "admin" || got.Token != "a-token-value" || got.SNMP() != (wire.SNMPCredential{Community: "c0mmunity-s3cret"}) {
		t.Errorf("WithSNMP = %v", got)
	}
	if !got.HasLogin() || v3User().HasLogin() || (credential.Credential{}).HasLogin() {
		t.Error("HasLogin is wrong for a token, an SNMP user alone or nothing")
	}
	for families, want := range map[credential.Families]string{
		credential.LoginFamily: "login", credential.SNMPFamily: "SNMP",
		credential.LoginFamily | credential.SNMPFamily: "login and SNMP", 0: "no family",
	} {
		if families.String() != want {
			t.Errorf("Families(%d).String() = %q, want %q", families, families.String(), want)
		}
	}
}

// TestListCredentials_NamesTheSNMPKinds: a community, a v3 user's
// authentication and its privacy are listed as kinds, with the USM user and
// a short community's mark, and never a value.
func TestListCredentials_NamesTheSNMPKinds(t *testing.T) {
	dir := t.TempDir()
	key := testKey('a')
	withLogin := v3User()
	withLogin.Username, withLogin.Password = "admin", "a-real-password"
	if err := credential.SaveFileStore(dir, key, "r1", withLogin); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "r2", credential.Credential{SNMPCommunity: "public"}, credential.SaveOptions{AllowShortCommunity: true}); err != nil {
		t.Fatal(err)
	}
	listed, err := credential.ListCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d credentials, want 2", len(listed))
	}
	if got := strings.Join(listed[0].Kinds, ","); got != "password,snmp_v3_auth,snmp_v3_priv" || listed[0].SNMPUser != "monitor" || listed[0].ShortCommunity {
		t.Errorf("r1 listed as %+v", listed[0])
	}
	if got := strings.Join(listed[1].Kinds, ","); got != "snmp_community" || !listed[1].ShortCommunity {
		t.Errorf("r2 listed as %+v", listed[1])
	}
}
