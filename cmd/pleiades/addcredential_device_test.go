// Tests for add-credential with a name the inventory does not have, through
// the real binary (decided 2026-11-11, the 0.3.x half): stored with a
// warning and marked unbound, --no-device accepted, near matches named, the
// mark shown by list-credentials, and announced by the first run that uses
// the credential.
package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// credentialProject returns a project holding the device web01.
func credentialProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web01", "--type", "linux_server", "--set", "host=127.0.0.1", "--set", "port=1"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	return dir
}

// listedCredentials returns list-credentials --json's entries by name.
func listedCredentials(t *testing.T, dir string) map[string]credential.CredentialInfo {
	t.Helper()
	out, err := runPleiades(t, dir, "list-credentials", "--json")
	if err != nil {
		t.Fatalf("list-credentials: %v\n%s", err, out)
	}
	var listed []credential.CredentialInfo
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list-credentials --json is not one JSON document: %v\n%s", err, out)
	}
	byName := map[string]credential.CredentialInfo{}
	for _, c := range listed {
		byName[c.Name] = c
	}
	return byName
}

// TestCLI_AddCredential_AMissingDeviceIsStoredUnboundWithAWarning: a name the
// inventory does not have is stored, as before, with a warning that names it
// and the 0.4.0 refusal, and the entry is marked unbound.
func TestCLI_AddCredential_AMissingDeviceIsStoredUnboundWithAWarning(t *testing.T) {
	dir := credentialProject(t)
	out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "db07", "--username", "u", "--password-stdin")
	if err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	for _, want := range []string{`no device named "db07"`, "--no-device", "0.4.0", "unbound"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "did you mean") {
		t.Errorf("a name with no near match named one:\n%s", out)
	}
	if _, err := credential.NewLazyFileStore(dir).Lookup(context.Background(), "db07"); err != nil {
		t.Fatalf("the credential was not stored: %v", err)
	}
	if !listedCredentials(t, dir)["db07"].Unbound {
		t.Error("the credential is not marked unbound")
	}
}

// TestCLI_AddCredential_NamesNearMatches: a typo of a device's name is
// warned about with the device it was probably meant for, case first.
func TestCLI_AddCredential_NamesNearMatches(t *testing.T) {
	dir := credentialProject(t)
	for name, want := range map[string]string{"web1": `did you mean "web01"`, "WEB01": `did you mean "web01"`} {
		out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", name, "--username", "u", "--password-stdin")
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: the warning does not say %q:\n%s", name, want, out)
		}
	}
}

// TestCLI_AddCredential_NoDeviceIsAcceptedAndChecked: --no-device stores a
// name the inventory does not have with no warning, marked unbound; given
// for a device the inventory has, it is refused before the secret is read,
// and nothing is stored; and a device the inventory has is stored bound with
// no warning.
func TestCLI_AddCredential_NoDeviceIsAcceptedAndChecked(t *testing.T) {
	dir := credentialProject(t)
	out, err := runPleiadesWithStdin(t, dir, "plugin-token\n", "add-credential", "netbox", "--token-stdin", "--no-device")
	if err != nil {
		t.Fatalf("--no-device: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning") {
		t.Errorf("--no-device still warned:\n%s", out)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out, err = runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "web01", "--username", "u", "--password-stdin", "--no-device")
	if err == nil || !strings.Contains(out, `device "web01" is in the inventory, so --no-device does not apply`) {
		t.Errorf("--no-device for a device the inventory has: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")); string(after) != string(before) {
		t.Error("a refused add-credential changed the vault")
	}
	out, err = runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "web01", "--username", "u", "--password-stdin")
	if err != nil || strings.Contains(out, "warning") {
		t.Errorf("a device the inventory has: %v\n%s", err, out)
	}
	listed := listedCredentials(t, dir)
	if !listed["netbox"].Unbound || listed["web01"].Unbound {
		t.Errorf("marks: netbox %+v, web01 %+v; want netbox unbound and web01 bound", listed["netbox"], listed["web01"])
	}
	if got := listed["netbox"].Kinds; len(got) != 1 || got[0] != "token" {
		t.Errorf("netbox's kinds = %v, want [token]", got)
	}
}

// TestCLI_ListCredentials_ShowsUnboundNeverAValue: the text listing marks an
// unbound entry and shows no secret.
func TestCLI_ListCredentials_ShowsUnboundNeverAValue(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "list-s3cret\n", "add-credential", "later01", "--username", "ops", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	out, err := runPleiades(t, dir, "list-credentials")
	if err != nil {
		t.Fatalf("list-credentials: %v\n%s", err, out)
	}
	if !strings.Contains(out, "later01  ops  password  unbound") || strings.Contains(out, "list-s3cret") {
		t.Errorf("list-credentials:\n%s", out)
	}
	if out, err := runPleiades(t, t.TempDir(), "list-credentials"); err != nil || !strings.Contains(out, "no device credentials") {
		t.Errorf("an empty project: %v\n%s", err, out)
	}
}

// TestCLI_AddHost_BindsACredentialStoredBeforeIt: a credential stored before
// its device exists is bound by the add-host that adds the device, which says
// so naming the account and how to remove it if it was not meant for the
// device, so the listing no longer calls it unbound and no run need announce
// it. It used to stay listed unbound until a run used it.
func TestCLI_AddHost_BindsACredentialStoredBeforeIt(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "late01", "--username", "ops", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if out, _ := runPleiades(t, dir, "list-credentials"); !strings.Contains(out, "unbound: no device of this name; add-host binds it when one is added") {
		t.Errorf("the listing does not say how an unbound credential is bound:\n%s", out)
	}
	out, err := runPleiades(t, dir, "add-host", "late01", "--type", "linux_server", "--set", "host=127.0.0.1", "--set", "port=1")
	if err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	for _, want := range []string{"bound the credential stored for late01 before it existed (account ops)", "pleiades remove-credential late01"} {
		if !strings.Contains(out, want) {
			t.Errorf("add-host does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("add-host printed the secret:\n%s", out)
	}
	if listed := listedCredentials(t, dir)["late01"]; listed.Unbound {
		t.Errorf("the credential is still unbound after add-host: %+v", listed)
	}
	// A host with no credential stored before it says nothing of one.
	if out, err := runPleiades(t, dir, "add-host", "plain01", "--type", "linux_server", "--set", "host=127.0.0.2"); err != nil || strings.Contains(out, "bound the credential") {
		t.Errorf("add-host with no stored credential: %v\n%s", err, out)
	}
}

// TestCLI_Run_TheFirstRunUsingAnUnboundCredentialSaysSo: a credential is
// stored before its device exists, then the device arrives other than by
// add-host (an edit to inventory.yaml, as an import or a sync adds one), and
// a run uses it. The listing says the device now exists; that run warns once
// and clears the mark; the next run does not warn.
func TestCLI_Run_TheFirstRunUsingAnUnboundCredentialSaysSo(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "late01", "--username", "u", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	path := filepath.Join(dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	hosts = append(hosts, inventory.HostSpec{ID: "late01-id", Name: "late01", Type: "linux_server", Properties: map[string]any{"host": "127.0.0.1", "port": 1}})
	if err := inventory.WriteHosts(path, hosts); err != nil {
		t.Fatal(err)
	}
	if listed := listedCredentials(t, dir)["late01"]; !listed.Unbound || !listed.DeviceExists {
		t.Errorf("the listing of an unbound credential whose device arrived: %+v", listed)
	}
	if out, _ := runPleiades(t, dir, "list-credentials"); !strings.Contains(out, "a device of this name now exists: its first run binds it and says so") {
		t.Errorf("the text listing does not say the device exists:\n%s", out)
	}
	runbook := filepath.Join(dir, "runbooks", "touch.yaml")
	if err := os.WriteFile(runbook, []byte("# Reads the uptime of late01; changes nothing.\nid: touch\nhosts: late01\ntasks:\n  - name: uptime\n    exec.command:\n      cmd: uptime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The device is not there (port 1), so the run fails after the
	// credential is read; what is under test is what the run says first.
	first, _ := runPleiades(t, dir, "--known-hosts", knownHosts, "run", runbook)
	if !strings.Contains(first, `device "late01" is using a credential stored before any inventory device had that name`) {
		t.Fatalf("the first run did not say so:\n%s", first)
	}
	if listedCredentials(t, dir)["late01"].Unbound {
		t.Error("the mark was not cleared after the run said so")
	}
	second, _ := runPleiades(t, dir, "--known-hosts", knownHosts, "run", runbook)
	if strings.Contains(second, "stored before any inventory device") {
		t.Errorf("the second run said so again:\n%s", second)
	}
}
