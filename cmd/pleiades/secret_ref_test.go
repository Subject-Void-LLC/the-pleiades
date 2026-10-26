// Gate for named secrets through the real binary: add-secret stores a
// pre-shared key and a CA passphrase, a runbook names them as secret
// references, and a scripted IOS router over real SSH receives the values
// while the run's report and journal never hold them. The router is a script,
// so this proves what Pleiades sends and reports, never what IOS does with a
// key; the live CML gate proves that.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// The secrets the gate stores, each long enough to store and free of
// anything a command line would split on.
const (
	gatePSK        = "Psk-Gate-Value-1234"
	gatePassphrase = "Ca-Gate-Passphrase-5678"
	gateStart      = "crypto pki server CVD start"
)

// secretRouter starts a scripted IOS router whose CA start asks for the
// passphrase, as IOS's does.
func secretRouter(t *testing.T) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "r1#",
		Config: &remoteexectest.ConfigMode{Enter: "configure terminal", Exit: "end", Prompt: "r1(config)#"},
		// show gate raw prints the key with no word before it, so only
		// masking by value can hide it (the review's F7).
		Replies: map[string]string{"show running-config": "hostname r1\r\nend", "show gate raw": "  " + gatePSK},
		Asks:    map[string][]remoteexectest.Question{gateStart: {{Text: "Password: ", Want: gatePassphrase, Declined: "% Bad passphrase"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// secretProject is a project whose r1 is srv, with r1's login and both
// secrets stored, and a home whose known_hosts names srv.
func secretProject(t *testing.T, srv *remoteexectest.Server) (string, string) {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"add-host", "r1", "--type", "cisco_router", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, srv.Password+"\n", "add-credential", "r1", "--username", srv.Username, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	for field, value := range map[string]string{"dmvpn psk": gatePSK, "ca passphrase": gatePassphrase} {
		name, f, _ := strings.Cut(field, " ")
		out, err := runPleiadesWithStdin(t, dir, value+"\n", "add-secret", name, "--field", f, "--value-stdin")
		if err != nil || strings.Contains(out, value) {
			t.Fatalf("add-secret %s: %v\n%s", field, err, out)
		}
	}
	return dir, home
}

// writeSecretRunbook writes body as runbooks/name.yaml in dir.
func writeSecretRunbook(t *testing.T, dir, name, body string) string {
	t.Helper()
	rel := filepath.Join("runbooks", name+".yaml")
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

// projectHolds reports whether any file under dir holds value.
func projectHolds(t *testing.T, dir, value string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), value) { // #nosec G304 -- this test's own directory
				hits = append(hits, p)
			}
		}
		return nil
	})
	return hits
}

// secretRunbook configures a keyring from the pre-shared key and starts a CA
// answering its passphrase from the other secret.
const secretRunbook = `# What this does: sets an IKEv2 keyring's pre-shared key and starts a CA, both from named secrets.
# Blast radius: r1 only. Changes: r1's running configuration. Undo: none (a gate).
id: secret-gate
hosts: r1
tasks:
  - name: Set the keyring
    net.ios.config:
      checkpoint: false
      insecure_telnet: false
      lines:
        - crypto ikev2 keyring DMVPN
        - " peer ANY"
        - "  pre-shared-key {{ secret.dmvpn.psk }}"
  - name: Start the CA
    net.ios.command:
      command: ` + gateStart + `
      prompts:
        - match: 'Password:'
          answer: "{{ secret.ca.passphrase }}"
`

// TestCLI_SecretReferencesReachTheDeviceOnly is the gate.
func TestCLI_SecretReferencesReachTheDeviceOnly(t *testing.T) {
	t.Run("the router receives both values, and nothing the run keeps holds either", func(t *testing.T) {
		srv := secretRouter(t)
		dir, home := secretProject(t, srv)
		rb := writeSecretRunbook(t, dir, "secret-gate", secretRunbook)
		if out, err := runPleiadesWithHome(t, dir, home, "validate", rb); err != nil || !strings.Contains(out, "no issues found") {
			t.Fatalf("validate: %v\n%s", err, out)
		}
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", rb)
		if code != 0 || rep.Outcome.Status != "complete" {
			t.Fatalf("exit %d, outcome %+v\n%s", code, rep.Outcome, raw)
		}
		sent := strings.Join(srv.Commands(), "\n")
		for _, want := range []string{"pre-shared-key " + gatePSK, gatePassphrase} {
			if !strings.Contains(sent, want) {
				t.Errorf("the router never received %q:\n%s", want, sent)
			}
		}
		for _, value := range []string{gatePSK, gatePassphrase} {
			if strings.Contains(raw, value) {
				t.Errorf("the run report holds a secret's value")
			}
			if hits := projectHolds(t, dir, value); len(hits) > 0 {
				t.Errorf("the project holds a secret's value in the clear: %v", hits)
			}
		}
		found := false
		for _, task := range rep.Tasks {
			if task.Name != "Start the CA" {
				continue
			}
			found = true
			exchanges, _ := task.Stats["exchanges"].([]any)
			if len(exchanges) != 1 || exchanges[0].(map[string]any)["answer"] != "(secret)" {
				t.Errorf("the CA's exchanges are %v, want one answer recorded as (secret)", task.Stats["exchanges"])
			}
		}
		if !found {
			t.Errorf("the report has no task named Start the CA:\n%s", raw)
		}
		text, err := runPleiadesWithHome(t, dir, home, "run", rb)
		if err != nil {
			t.Fatalf("a second run: %v\n%s", err, text)
		}
		for _, value := range []string{gatePSK, gatePassphrase} {
			if strings.Contains(text, value) {
				t.Errorf("the run's text output holds a secret's value")
			}
		}

		// A run that resolves no reference, and reads a show printing the key
		// bare: only the project's named secrets, masked from the run's start,
		// can hide it.
		show := writeSecretRunbook(t, dir, "show-raw", "# What this does: reads a show that prints the key bare.\n# Blast radius: r1, read only. Changes: nothing. Undo: none.\nid: show-raw\nhosts: r1\ntasks:\n  - name: raw\n    net.ios.command:\n      command: show gate raw\n")
		showRep, showRaw, showCode := runPleiadesJSONWithHome(t, dir, home, "run", show)
		if showCode != 0 || showRep.Outcome.Status != "complete" {
			t.Fatalf("the show run: exit %d, %+v\n%s", showCode, showRep.Outcome, showRaw)
		}
		if strings.Contains(showRaw, gatePSK) {
			t.Error("a run that resolved no reference reported the key a show printed bare")
		}
	})

	t.Run("validation refuses a reference where the engine would render it, or beside another expression", func(t *testing.T) {
		dir, home := secretProject(t, secretRouter(t))
		for name, body := range map[string]string{
			"in command text the method does not resolve": "id: bad-command\nhosts: r1\ntasks:\n  - name: x\n    net.ios.command:\n      command: \"show {{ secret.dmvpn.psk }}\"\n",
			"beside a variable":                           "id: bad-mixed\nhosts: r1\ntasks:\n  - name: x\n    net.ios.config:\n      lines:\n        - \"pre-shared-key {{ secret.dmvpn.psk }} {{ vars.x | cli_token }}\"\n",
			"with a filter":                               "id: bad-filter\nhosts: r1\ntasks:\n  - name: x\n    net.ios.config:\n      lines:\n        - \"pre-shared-key {{ secret.dmvpn.psk | quote }}\"\n",
		} {
			rb := writeSecretRunbook(t, dir, strings.ReplaceAll(name, " ", "-"), "# What this does: a refused runbook.\n# Blast radius: none. Changes: nothing. Undo: none.\n"+body)
			out, err := runPleiadesWithHome(t, dir, home, "validate", rb)
			if err == nil || !strings.Contains(out, "secret reference") {
				t.Errorf("%s: validate passed or said nothing of the reference: %v\n%s", name, err, out)
			}
		}
	})

	t.Run("a variable cannot write a reference the method would resolve", func(t *testing.T) {
		srv := secretRouter(t)
		dir, home := secretProject(t, srv)
		rb := writeSecretRunbook(t, dir, "inject", "# What this does: tries to read a secret through a variable.\n# Blast radius: r1. Changes: nothing. Undo: none.\n"+
			"id: inject\nhosts: r1\ntasks:\n  - name: Start the CA\n    net.ios.command:\n      command: "+gateStart+"\n      prompts:\n        - match: 'Password:'\n          answer: \"{{ vars.x }}\"\n")
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", rb, "-e", "x={{ secret.ca.passphrase }}")
		if code == 0 || rep.Outcome.Status == "complete" || !strings.Contains(raw, "only the runbook's author") {
			t.Fatalf("exit %d, outcome %+v: the rendered reference was not refused\n%s", code, rep.Outcome, raw)
		}
		if strings.Contains(strings.Join(srv.Commands(), "\n"), gatePassphrase) {
			t.Fatal("the router received the passphrase through a variable")
		}
	})
}
