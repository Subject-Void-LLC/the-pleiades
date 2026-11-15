// Tests of add-credential's SNMP family and its certificate check, through
// the real binary: each family saved apart from the other, every secret read
// from one pipe in one order, a short community refused unless allowed and
// then warned about by every run, and a private key refused in the
// certificate field.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestCLI_AddCredential_SNMPIsSavedApartFromTheLogin: a login and an SNMPv3
// user piped in one call are both stored, every line in its place; SNMP
// flags alone then switch the device to a community, leaving no v3 secret
// behind and the login as it was; and a login saved after keeps the
// community.
func TestCLI_AddCredential_SNMPIsSavedApartFromTheLogin(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "login-pass-1\nen4ble-s3cret\nauth-pass-phrase\npriv-pass-phrase\n",
		"add-credential", "web01", "--username", "admin", "--password-stdin", "--enable-password-stdin",
		"--snmp-user", "monitor", "--snmp-auth", "sha512", "--snmp-priv", "aes192-reeder", "--snmp-passphrase-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	c := storedCredential(t, dir, "web01")
	if c.Password != "login-pass-1" || c.EnablePassword != "en4ble-s3cret" || c.SNMPUser != "monitor" ||
		c.SNMPAuthProtocol != wire.SNMPAuthSHA512 || c.SNMPAuthPassphrase != "auth-pass-phrase" ||
		c.SNMPPrivProtocol != wire.SNMPPrivAES192Reeder || c.SNMPPrivPassphrase != "priv-pass-phrase" {
		t.Fatalf("one pipe stored %v", c)
	}

	out, err := runPleiadesWithStdin(t, dir, "c0mmunity-s3cret\n", "add-credential", "web01", "--snmp-community-stdin")
	if err != nil || !strings.Contains(out, "a login stored for it is kept") {
		t.Fatalf("switching to a community: %v\n%s", err, out)
	}
	c = storedCredential(t, dir, "web01")
	if c.SNMPCommunity != "c0mmunity-s3cret" || c.SNMPUser != "" || c.SNMPAuthPassphrase != "" || c.SNMPPrivPassphrase != "" {
		t.Errorf("after switching to a community the SNMP family is %v", c.SNMP())
	}
	if c.Password != "login-pass-1" || c.EnablePassword != "en4ble-s3cret" {
		t.Errorf("saving SNMP alone changed the login: %v", c)
	}

	if out, err := runPleiadesWithStdin(t, dir, "login-pass-2\n", "add-credential", "web01", "--username", "admin", "--password-stdin"); err != nil {
		t.Fatalf("saving a login: %v\n%s", err, out)
	}
	if c = storedCredential(t, dir, "web01"); c.Password != "login-pass-2" || c.EnablePassword != "" || c.SNMPCommunity != "c0mmunity-s3cret" {
		t.Errorf("a login save should replace the login whole and keep the community: %v", c)
	}
	if listed := listedCredentials(t, dir)["web01"]; strings.Join(listed.Kinds, ",") != "password,snmp_community" {
		t.Errorf("listed kinds %v", listed.Kinds)
	}
}

// TestCLI_AddCredential_SNMPRefusals: every combination no agent accepts is
// refused naming the flag, before any secret is read when the flags alone
// decide it, and a refused call leaves the vault as it was.
func TestCLI_AddCredential_SNMPRefusals(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "login-pass-1\n", "add-credential", "web01", "--username", "admin", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"a community and a user":      {"c0mmunity-s3cret\n", []string{"--snmp-community-stdin", "--snmp-user", "u"}, "one or the other"},
		"a community read two ways":   {"c0mmunity-s3cret\n", []string{"--snmp-community", "--snmp-community-stdin"}, "mutually exclusive"},
		"an allowance alone":          {"", []string{"--allow-short-community", "--snmp-user", "u"}, "--allow-short-community applies only"},
		"privacy without auth, piped": {"x\n", []string{"--snmp-user", "u", "--snmp-priv", "aes128", "--snmp-passphrase-stdin"}, "--snmp-passphrase-stdin applies only"},
		"privacy without auth":        {"", []string{"--snmp-user", "u", "--snmp-priv", "aes128"}, "--snmp-priv is set without an authentication protocol"},
		"a protocol without a user":   {"", []string{"--snmp-auth", "sha"}, "--snmp-user is empty"},
		"an unknown protocol":         {"", []string{"--snmp-user", "u", "--snmp-auth", "SHA"}, "--snmp-auth is not one of md5, sha"},
		"a short passphrase":          {"short77\n", []string{"--snmp-user", "u", "--snmp-auth", "md5", "--snmp-passphrase-stdin"}, "the SNMP authentication passphrase is shorter than 8"},
		"a short community":           {"public\n", []string{"--snmp-community-stdin"}, "--allow-short-community"},
		"an allowance it cannot need": {"c0mmunity-s3cret\n", []string{"--snmp-community-stdin", "--allow-short-community"}, "allows nothing"},
	} {
		out, err := runPleiadesWithStdin(t, dir, tc.stdin, append([]string{"add-credential", "web01"}, tc.args...)...)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %v\n%s; want a refusal saying %q", name, err, out, tc.want)
		}
		for _, secret := range []string{"c0mmunity-s3cret", "short77", "public"} {
			if strings.Contains(tc.stdin, secret) && strings.Contains(out, secret) {
				t.Errorf("%s: the refusal repeats a secret:\n%s", name, out)
			}
		}
		if after, _ := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")); string(after) != string(before) {
			t.Errorf("%s: a refused add-credential changed the vault", name)
		}
	}
}

// TestCLI_Run_EveryRunUsingAShortCommunitySaysSo: a community under eight
// characters is stored only with --allow-short-community, which warns, is
// listed as short, and every run that reads the credential warns that the
// community is masked by shape only, the second run as much as the first.
func TestCLI_Run_EveryRunUsingAShortCommunitySaysSo(t *testing.T) {
	dir := credentialProject(t)
	out, err := runPleiadesWithStdin(t, dir, "login-pass-1\npublic\n", "add-credential", "web01", "--username", "u", "--password-stdin", "--snmp-community-stdin", "--allow-short-community")
	if err != nil || !strings.Contains(out, "masks it only by its shape") {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if listed := listedCredentials(t, dir)["web01"]; !listed.ShortCommunity {
		t.Errorf("the short community is not marked: %+v", listed)
	}
	if text, _ := runPleiades(t, dir, "list-credentials"); !strings.Contains(text, "short community") || strings.Contains(text, "login-pass-1") {
		t.Errorf("list-credentials:\n%s", text)
	}
	runbook := filepath.Join(dir, "runbooks", "touch.yaml")
	if err := os.WriteFile(runbook, []byte("# Reads the uptime of web01; changes nothing.\nid: touch\nhosts: web01\ntasks:\n  - name: uptime\n    exec.command:\n      cmd: uptime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The device is not there (port 1), so each run fails after the
	// credential is read; what is under test is what each run says.
	for _, run := range []string{"the first run", "the second run"} {
		said, _ := runPleiades(t, dir, "--known-hosts", knownHosts, "run", runbook)
		if !strings.Contains(said, `device "web01"'s SNMP community is shorter than 8 characters`) || !strings.Contains(said, "never by value") {
			t.Errorf("%s did not say so:\n%s", run, said)
		}
	}
}

// TestCLI_AddCredential_RefusesAKeyInTheCertificateFile: a certificate file
// holding the key beside the certificate is refused naming --key, and
// nothing is stored; the certificate alone, with its key in its own file, is
// stored.
func TestCLI_AddCredential_RefusesAKeyInTheCertificateFile(t *testing.T) {
	dir := credentialProject(t)
	const certPEM = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ\n-----END CERTIFICATE-----\n"
	const keyPEM = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----\n"
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	key := filepath.Join(t.TempDir(), "client.key")
	cert := filepath.Join(t.TempDir(), "client.pem")
	for path, body := range map[string]string{bundle: certPEM + keyPEM, key: keyPEM, cert: certPEM} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runPleiades(t, dir, "add-credential", "web01", "--certificate", bundle, "--key", key)
	if err == nil || !strings.Contains(out, "give the private key to --key") || strings.Contains(out, "MIGHAgEAMBMGByqGSM49") {
		t.Fatalf("a bundle in --certificate: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".pleiades", "credentials.yaml")); statErr == nil {
		t.Error("a refused certificate wrote the credential file")
	}
	if out, err := runPleiades(t, dir, "add-credential", "web01", "--certificate", cert, "--key", key); err != nil {
		t.Fatalf("a certificate with its key apart: %v\n%s", err, out)
	}
	if c := storedCredential(t, dir, "web01"); string(c.CertificatePEM) != certPEM || string(c.PrivateKeyPEM) != keyPEM {
		t.Errorf("stored certificate %d bytes and key %d bytes", len(c.CertificatePEM), len(c.PrivateKeyPEM))
	}
}
