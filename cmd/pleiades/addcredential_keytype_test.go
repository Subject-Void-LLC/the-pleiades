// Tests of add-credential --generate's choices, in process so a test can
// count the keys made and see what standard input was left: the key type
// stored and printed, the RSA note, --no-password, an enable secret, and
// every refusal made before any input is read or any key is made.
package main

import (
	"encoding/pem"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// countKeys makes generateKey count the keys it makes for the rest of t.
func countKeys(t *testing.T) *int {
	t.Helper()
	made := 0
	original := generateKey
	generateKey = func(username, comment string, opts credential.GenerateOptions) (credential.Credential, error) {
		made++
		return original(username, comment, opts)
	}
	t.Cleanup(func() { generateKey = original })
	return &made
}

// withStdinLeft runs fn with standard input reading text and returns what
// fn left unread.
func withStdinLeft(t *testing.T, text string, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved; _ = r.Close() }()
	fn()
	left, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(left)
}

// generateIn runs add-credential --generate for device in dir with args,
// returning its stdout, its stderr and its error.
func generateIn(t *testing.T, dir, device string, args ...string) (string, string, error) {
	t.Helper()
	var runErr error
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			runErr = runAddCredential(append([]string{device, "--dir", dir, "--no-device", "--generate"}, args...))
		})
	})
	return stdout, stderr, runErr
}

// generateProject returns an initialized project directory.
func generateProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAddCredential_GenerateStoresAndPrintsTheKeyTypeAskedFor: each type is
// stored in OpenSSH's form as that type, the output says what was stored,
// then the type, then the stored key's fingerprint, with the public key line
// last, and holds no secret; only an RSA key prints the signature note, on
// stderr.
func TestAddCredential_GenerateStoresAndPrintsTheKeyTypeAskedFor(t *testing.T) {
	dir := generateProject(t)
	for _, tc := range []struct {
		args []string
		want credential.KeyType
	}{
		{nil, credential.KeyTypeEd25519},
		{[]string{"--key-type", "rsa-3072"}, credential.KeyTypeRSA3072},
		{[]string{"--key-type", "ecdsa-p384"}, credential.KeyTypeECDSAP384},
	} {
		device := "lab-" + string(tc.want)
		stdout, stderr, err := generateIn(t, dir, device, append([]string{"--username", "root"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%s: %v", tc.want, err)
		}
		stored := lookupStored(t, dir, device)
		if block, _ := pem.Decode(stored.PrivateKeyPEM); block == nil || block.Type != "OPENSSH PRIVATE KEY" {
			t.Fatalf("%s: the stored key is not in OpenSSH form", tc.want)
		}
		info, err := credential.DescribeKey(stored)
		if err != nil || info.Type != tc.want {
			t.Fatalf("%s: stored %+v, %v", tc.want, info, err)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		wantLines := []string{
			"stored a generated credential for device \"" + device + "\" (unbound: no inventory device has this name): an " + string(tc.want) + " key and a 24-character password, for root",
			"key type: " + string(tc.want),
			"fingerprint: " + info.Fingerprint,
		}
		if len(lines) != 4 || strings.Join(lines[:3], "\n") != strings.Join(wantLines, "\n") || !strings.HasPrefix(lines[3], "public key: ") {
			t.Errorf("%s printed:\n%s\nwant:\n%s\npublic key: ...", tc.want, stdout, strings.Join(wantLines, "\n"))
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimPrefix(lines[len(lines)-1], "public key: ")))
		if err != nil || ssh.FingerprintSHA256(pub) != info.Fingerprint {
			t.Errorf("%s: the public key line is not the stored key's: %v", tc.want, err)
		}
		if strings.Contains(stdout+stderr, stored.Password) || strings.Contains(stdout+stderr, "PRIVATE KEY") {
			t.Errorf("%s: the output shows a secret", tc.want)
		}
		if noted := strings.Contains(stderr, "ssh_allow_legacy_user_key_signatures"); noted != tc.want.IsRSA() {
			t.Errorf("%s: the RSA note printed %v, stderr:\n%s", tc.want, noted, stderr)
		}
		if tc.want.IsRSA() && strings.Contains(stdout, "ssh_allow_legacy") {
			t.Errorf("the RSA note went to stdout:\n%s", stdout)
		}
	}
}

// TestAddCredential_GenerateWithNoPasswordAndAnEnableSecret: --no-password
// stores an empty password and says so; an enable secret, piped or not,
// now goes with --generate, stored and named but never shown.
func TestAddCredential_GenerateWithNoPasswordAndAnEnableSecret(t *testing.T) {
	dir := generateProject(t)
	stdout, _, err := generateIn(t, dir, "sw1", "--username", "admin", "--no-password")
	if err != nil {
		t.Fatal(err)
	}
	if stored := lookupStored(t, dir, "sw1"); stored.Password != "" || len(stored.PrivateKeyPEM) == 0 {
		t.Errorf("--no-password stored %s", stored)
	}
	if !strings.Contains(stdout, "an ed25519 key and no password, for admin") {
		t.Errorf("--no-password printed:\n%s", stdout)
	}

	left := withStdinLeft(t, "en4ble-s3cret\n", func() {
		stdout, _, err = generateIn(t, dir, "sw2", "--username", "admin", "--no-password", "--enable-password-stdin")
	})
	if err != nil || left != "" {
		t.Fatalf("--enable-password-stdin: %v, left %q", err, left)
	}
	stored := lookupStored(t, dir, "sw2")
	if stored.EnablePassword != "en4ble-s3cret" || stored.Password != "" {
		t.Errorf("stored enable %v, password %v", stored.EnablePassword == "en4ble-s3cret", stored.Password != "")
	}
	if !strings.Contains(stdout, "an ed25519 key, no password and the enable secret given, for admin") || strings.Contains(stdout, "en4ble-s3cret") {
		t.Errorf("an enable secret printed:\n%s", stdout)
	}
}

// TestAddCredential_GenerateRefusesBeforeReadingOrMakingAnything: a bad key
// type, a --generate flag without --generate, and a stored login without
// --replace are each refused with standard input untouched, the store as it
// was, and no key made.
func TestAddCredential_GenerateRefusesBeforeReadingOrMakingAnything(t *testing.T) {
	dir := generateProject(t)
	if _, _, err := generateIn(t, dir, "db1", "--username", "root"); err != nil {
		t.Fatal(err)
	}
	before := lookupStored(t, dir, "db1")
	made := countKeys(t)
	const piped = "c0mmunity-s3cret\n"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"rsa-2048":                    {[]string{"new1", "--generate", "--key-type", "rsa-2048", "--snmp-community-stdin"}, "use rsa-3072"},
		"bare rsa":                    {[]string{"new1", "--generate", "--key-type", "rsa", "--snmp-community-stdin"}, "not a key type this generates"},
		"upper case":                  {[]string{"new1", "--generate", "--key-type", "ED25519", "--snmp-community-stdin"}, "not a key type this generates"},
		"dsa":                         {[]string{"new1", "--generate", "--key-type", "dsa", "--snmp-community-stdin"}, "not a key type this generates"},
		"a secret pasted as the type": {[]string{"new1", "--generate", "--key-type", "Hunter2-s3cret-pasted", "--snmp-community-stdin"}, "not a key type this generates"},
		"key type without generate":   {[]string{"new1", "--key-type", "rsa-3072", "--password-stdin"}, "--key-type only applies to --generate"},
		"no password without it":      {[]string{"new1", "--no-password", "--password-stdin"}, "--no-password only applies to --generate"},
		"json without it":             {[]string{"new1", "--json", "--password-stdin"}, "--json only applies to --generate"},
		"replace without it":          {[]string{"new1", "--replace", "--password-stdin"}, "--replace only applies to --generate"},
		"a stored login, no replace":  {[]string{"db1", "--generate", "--enable-password-stdin", "--snmp-community-stdin"}, "--replace discards it"},
	} {
		var err error
		left := withStdinLeft(t, piped+piped, func() {
			err = runAddCredential(append([]string{tc.args[0], "--dir", dir, "--no-device", "--username", "root"}, tc.args[1:]...))
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "Hunter2") {
			t.Errorf("%s: the refusal repeats the value given: %v", name, err)
		}
		if left != piped+piped {
			t.Errorf("%s: read standard input before refusing, leaving %q", name, left)
		}
	}
	if *made != 0 {
		t.Errorf("refused commands made %d keys", *made)
	}
	if after := lookupStored(t, dir, "db1"); after.Password != before.Password || string(after.PrivateKeyPEM) != string(before.PrivateKeyPEM) {
		t.Error("a refused generate changed the stored credential")
	}
	if listed, err := credential.ListCredentials(dir); err != nil || len(listed) != 1 {
		t.Errorf("refused commands stored %d credentials: %v", len(listed), err)
	}
}
