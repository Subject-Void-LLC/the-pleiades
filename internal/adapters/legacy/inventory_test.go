package legacy_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestBuildInventoryJSON_UntaggedDevice proves an untagged device is
// placed directly under "all.hosts", the shape a device with no group
// membership needs (Ansible's "yaml" inventory plugin, not the
// dynamic-inventory-script "_meta" shape -- see inventory.go's own
// top-of-file comment for why the distinction is load-bearing).
func TestBuildInventoryJSON_UntaggedDevice(t *testing.T) {
	payload := wire.DispatchPayload{
		DeviceID:   "dev-1",
		DeviceName: "sw1",
		DeviceHost: "10.0.0.9",
	}
	doc, err := legacy.BuildInventoryJSON(payload)
	if err != nil {
		t.Fatalf("BuildInventoryJSON returned unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	all, ok := parsed["all"].(map[string]any)
	if !ok {
		t.Fatalf("document has no \"all\" key: %s", doc)
	}
	hosts, ok := all["hosts"].(map[string]any)
	if !ok {
		t.Fatalf("all.hosts missing for untagged device: %s", doc)
	}
	if _, ok := hosts["sw1"]; !ok {
		t.Errorf("all.hosts does not contain \"sw1\": %s", doc)
	}
	if _, ok := all["children"]; ok {
		t.Errorf("all.children present for an untagged device, want absent: %s", doc)
	}
}

// TestBuildInventoryJSON_TaggedDevice proves a tagged device becomes a
// real Ansible group under "all.children", one per tag, matching this
// codebase's own worked example pairing (examples/upgrade_ios/pleiades/
// inventory.yaml's "tags: [catalyst_lab]" <-> examples/upgrade_ios/
// ansible/inventory.ini's "[catalyst_lab]"), and that capabilities and
// tags both also land as redundant hostvars.
func TestBuildInventoryJSON_TaggedDevice(t *testing.T) {
	payload := wire.DispatchPayload{
		DeviceID:     "dev-2",
		DeviceName:   "sw2",
		DeviceHost:   "10.0.0.10",
		SSHPort:      2222,
		Tags:         []string{"catalyst_lab", "prod"},
		Capabilities: []capability.Name{capability.NameCiscoIOS, capability.NameSSHTransport},
		Secrets:      credential.Flatten(credential.Credential{Username: "svc-netauto", Password: "hunter2"}),
	}
	doc, err := legacy.BuildInventoryJSON(payload)
	if err != nil {
		t.Fatalf("BuildInventoryJSON returned unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	all := parsed["all"].(map[string]any)
	children, ok := all["children"].(map[string]any)
	if !ok {
		t.Fatalf("all.children missing for a tagged device: %s", doc)
	}
	for _, tag := range []string{"catalyst_lab", "prod"} {
		group, ok := children[tag].(map[string]any)
		if !ok {
			t.Fatalf("all.children missing group %q: %s", tag, doc)
		}
		hosts, ok := group["hosts"].(map[string]any)
		if !ok {
			t.Fatalf("group %q has no hosts: %s", tag, doc)
		}
		hv, ok := hosts["sw2"].(map[string]any)
		if !ok {
			t.Fatalf("group %q does not contain host sw2: %s", tag, doc)
		}
		if got, want := hv["ansible_host"], "10.0.0.10"; got != want {
			t.Errorf("group %q ansible_host = %v, want %v", tag, got, want)
		}
		if got, want := hv["ansible_port"], float64(2222); got != want {
			t.Errorf("group %q ansible_port = %v, want %v", tag, got, want)
		}
		if got, want := hv["ansible_user"], "svc-netauto"; got != want {
			t.Errorf("group %q ansible_user = %v, want %v", tag, got, want)
		}
		if got, want := hv["ansible_password"], "hunter2"; got != want {
			t.Errorf("group %q ansible_password = %v, want %v", tag, got, want)
		}
		caps, _ := hv["pleiades_capabilities"].([]any)
		if len(caps) != 2 {
			t.Errorf("group %q pleiades_capabilities = %v, want 2 entries", tag, hv["pleiades_capabilities"])
		}
	}
}

// TestBuildInventoryJSON_PassphraseProtectedKeyRejected proves a
// passphrase-protected private key fails BuildInventoryJSON with a clear,
// named error rather than producing an inventory Ansible could only ever
// hang trying to use non-interactively (this package's own named,
// deliberate Phase 17 limitation).
func TestBuildInventoryJSON_PassphraseProtectedKeyRejected(t *testing.T) {
	payload := wire.DispatchPayload{
		DeviceName: "sw3",
		DeviceHost: "10.0.0.11",
		Secrets: credential.Flatten(credential.Credential{
			Username:      "svc-netauto",
			PrivateKeyPEM: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"),
			Passphrase:    "key-passphrase",
		}),
	}
	_, err := legacy.BuildInventoryJSON(payload)
	if !errors.Is(err, legacy.ErrPassphraseProtectedKey) {
		t.Fatalf("BuildInventoryJSON error = %v, want ErrPassphraseProtectedKey", err)
	}
}

// TestBuildInventoryJSON_AcceptedByRealAnsible is the RULE 0 check for
// this file's own central, empirically-driven claim: a real
// ansible-playbook, given exactly the document BuildInventoryJSON
// produces, really parses it, really resolves the one host into the
// right group, and really reads back the hostvars this package attached,
// rather than merely being "structurally plausible JSON" nobody has
// actually run. Skipped when ansible-playbook is not on PATH, the same
// convention this repository's other reference-platform checks use.
func TestBuildInventoryJSON_AcceptedByRealAnsible(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook not found on PATH")
	}

	payload := wire.DispatchPayload{
		DeviceID:     "dev-4",
		DeviceName:   "sw4",
		DeviceHost:   "127.0.0.1",
		Tags:         []string{"catalyst_lab"},
		Capabilities: []capability.Name{capability.NameCiscoIOS},
	}
	doc, err := legacy.BuildInventoryJSON(payload)
	if err != nil {
		t.Fatalf("BuildInventoryJSON returned unexpected error: %v", err)
	}

	dir := t.TempDir()
	invPath := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(invPath, doc, 0o600); err != nil {
		t.Fatalf("failed to write inventory fixture: %v", err)
	}
	playbookPath := filepath.Join(dir, "probe.yml")
	playbook := "---\n- hosts: all\n  gather_facts: false\n  connection: local\n  tasks:\n" +
		"    - name: show group and hostvars\n      debug:\n" +
		"        msg: \"host={{ inventory_hostname }} groups={{ group_names }} caps={{ pleiades_capabilities }}\"\n"
	if err := os.WriteFile(playbookPath, []byte(playbook), 0o600); err != nil {
		t.Fatalf("failed to write playbook fixture: %v", err)
	}

	cmd := exec.Command("ansible-playbook", "-v", "-i", invPath, playbookPath)
	cmd.Env = append(os.Environ(), "ANSIBLE_FORCE_COLOR=false", "ANSIBLE_NOCOLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real ansible-playbook rejected the generated inventory: %v\n%s", err, out)
	}

	events := legacy.ParseStdout(out, fixedNow)
	if len(events) < 1 {
		t.Fatalf("expected at least one parsed event from the real run, got 0: %s", out)
	}
	if got, want := events[0].Host, "sw4"; got != want {
		t.Errorf("event host = %q, want %q (%s)", got, want, out)
	}
	if got, want := events[0].EventData.Message, "host=sw4 groups=['catalyst_lab'] caps=['CiscoIOS"; !strings.Contains(got, want) {
		t.Errorf("event message = %q, does not contain %q", got, want)
	}
}

// TestBuildInventoryJSON_RefusesCertificateMaterial covers what this adapter
// would otherwise have done silently and wrongly.
//
// internal/dispatch attaches the machine identity to every payload whatever
// adapter will run it, and Phase 78d widened that identity to carry a
// certificate and a PKCS#12 bundle. This path reads only the four original
// keys, so a certificate credential reached ansible-playbook as an SSH
// private key file with no username: a TLS client key is a perfectly valid
// PEM private key, so nothing downstream objected. It simply failed to
// authenticate, against the wrong protocol, for a reason nothing in the
// output could explain.
func TestBuildInventoryJSON_RefusesCertificateMaterial(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		secrets map[string]string
	}{
		{
			name: "a loose certificate and key",
			secrets: map[string]string{
				wire.SecretCertificatePEM: "-----BEGIN CERTIFICATE-----\nbody\n-----END CERTIFICATE-----\n",
				wire.SecretPrivateKeyPEM:  "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n",
			},
		},
		{
			name:    "a sealed bundle",
			secrets: map[string]string{wire.SecretPFXBase64: "MIIKzQIBAzCCCoc="},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := legacy.BuildInventoryJSON(wire.DispatchPayload{
				DeviceName: "win01", Secrets: tt.secrets,
			})
			if err == nil {
				t.Fatal("certificate material was accepted on the Ansible path")
			}
			if !errors.Is(err, legacy.ErrCertificateCredential) {
				t.Errorf("error = %v, want ErrCertificateCredential", err)
			}
			if !strings.Contains(err.Error(), "win01") {
				t.Errorf("error = %v, want it to name the device", err)
			}
		})
	}

	// A bundle carries a passphrase, and the certificate check runs first so
	// it is not reported as an unsupported SSH key passphrase: a true
	// sentence about the wrong thing.
	_, err := legacy.BuildInventoryJSON(wire.DispatchPayload{
		DeviceName: "win01",
		Secrets: map[string]string{
			wire.SecretPFXBase64:  "MIIKzQIBAzCCCoc=",
			wire.SecretPassphrase: "unlock-me",
		},
	})
	if errors.Is(err, legacy.ErrPassphraseProtectedKey) {
		t.Errorf("error = %v, want the certificate refusal rather than the SSH passphrase one", err)
	}
}
