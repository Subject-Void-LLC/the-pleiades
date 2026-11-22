// Tests through the real binary that a project whose master key was lost
// refuses every command that would make a new key beside what the old one
// sealed, naming what it holds and the key to restore, and that a new project
// still gets its key from its first command (FAILURE_PATTERNS 1344).
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// keyWriter is one command that stores something under the master key,
// making the key when a project has none, with what it reads on standard
// input; firstUse is set when it also works as a new project's first command.
type keyWriter struct {
	name     string
	stdin    string
	args     []string
	firstUse bool
}

// keyWriters is every such command, its input files written under t's
// temporary directory: a kubeconfig, a containerlab topology and a key, and a
// password sealed to another project's seal key.
func keyWriters(t *testing.T) []keyWriter {
	t.Helper()
	files := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(files, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	kubeconfig := write("kubeconfig", "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n"+
		"clusters:\n- name: k\n  cluster:\n    server: https://k.example:6443\nusers:\n- name: u\n  user:\n    token: s3cret-token\n")
	topology := write("topology-data.json", `{"name": "lab1", "type": "clab", "links": [], "nodes": {"r1": {"shortname": "r1", "kind": "linux", "image": "frr", "mgmt-ipv4-address": "172.20.30.11", "labels": {"containerlab": "lab1", "clab-node-name": "r1"}}}}`)
	sshKey := write("id", "PRIVATE-KEY-PEM")
	// A password sealed to another project, which add-credential opens only
	// after it has the master key, so the refusal comes first.
	other := t.TempDir()
	if out, err := runPleiades(t, other, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	pub, err := runPleiades(t, other, "seal-key", "--json")
	if err != nil {
		t.Fatalf("seal-key: %v\n%s", err, pub)
	}
	var view struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(pub[strings.Index(pub, "{"):]), &view); err != nil {
		t.Fatalf("seal-key --json: %v\n%s", err, pub)
	}
	sealedFile := filepath.Join(files, "password.sealed")
	if out, err := runPleiadesWithStdin(t, other, "s3cret-sealed\n", "seal", "--to", view.PublicKey, "--secret-stdin", "--out", sealedFile); err != nil {
		t.Fatalf("seal: %v\n%s", err, out)
	}
	return []keyWriter{
		{"add-credential", "s3cret-two\n", []string{"add-credential", "web02", "--username", "u", "--password-stdin", "--no-device"}, true},
		{"add-credential --generate", "", []string{"add-credential", "web03", "--username", "u", "--generate", "--no-device"}, true},
		{"add-credential --password-file", "", []string{"add-credential", "web04", "--username", "u", "--password-file", sealedFile, "--no-device"}, false},
		{"add-secret", "s3cret-psk2\n", []string{"add-secret", "ike2", "--field", "psk", "--value-stdin"}, true},
		{"add-registry-credential", "s3cret-reg\n", []string{"add-registry-credential", "registry.example.com", "--username", "u", "--password-stdin"}, true},
		{"add-database-connection", "s3cret-db\n", []string{"add-database-connection", "db", "--host", "db.example.com", "--user", "u", "--password-stdin"}, true},
		{"seal-key", "", []string{"seal-key"}, true},
		{"import-kubeconfig", "", []string{"import-kubeconfig", "prod", "--kubeconfig", kubeconfig}, true},
		{"import-containerlab", "", []string{"import-containerlab", "--topology", topology, "--lab", "lab1", "--username", "netops", "--key", sshKey}, true},
	}
}

// unsetMasterKeyEnv keeps PLEIADES_MASTER_KEY from the binary's environment,
// so the key file is the only key a project has.
func unsetMasterKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_ALostMasterKeyIsNeverReplaced: a project holding a credential and a
// named secret whose master key was then lost refuses every writer, naming the
// credential file, the key file and PLEIADES_MASTER_KEY and saying what to
// remove when the key is gone, shows none of its secrets, and is left exactly
// as it was with no new key. Restoring the key lets the same command work.
func TestCLI_ALostMasterKeyIsNeverReplaced(t *testing.T) {
	unsetMasterKeyEnv(t)
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-one\n", "add-credential", "web01", "--username", "u", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-psk\n", "add-secret", "ike", "--field", "psk", "--value-stdin"); err != nil {
		t.Fatalf("add-secret: %v\n%s", err, out)
	}
	key := filepath.Join(dir, ".pleiades", "master.key")
	saved, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	for _, w := range keyWriters(t) {
		t.Run(w.name, func(t *testing.T) {
			// Each writer meets the lost key on its own, whatever one before
			// it did.
			if err := os.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			before := vaultSnapshot(t, dir)
			out, err := runPleiadesWithStdin(t, dir, w.stdin, w.args...)
			if err == nil {
				t.Fatalf("%s in a project that lost its key succeeded:\n%s", w.name, out)
			}
			for _, want := range []string{"credentials.yaml (1 device credential, 1 named secret)", filepath.Join(".pleiades", "master.key"), "PLEIADES_MASTER_KEY", "no new key is made", "remove what you accept losing"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s's refusal does not say %q:\n%s", w.name, want, out)
				}
			}
			if strings.Contains(out, "s3cret") {
				t.Errorf("%s's refusal shows a secret:\n%s", w.name, out)
			}
			if _, statErr := os.Stat(key); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("%s made a new key beside the sealed credentials: %v", w.name, statErr)
			}
			if after := vaultSnapshot(t, dir); !bytes.Equal(before, after) {
				t.Errorf("%s changed the project though it was refused", w.name)
			}
		})
	}
	// A listing, which decrypts nothing and needs no key, still works, and
	// makes no key either.
	if out, err := runPleiades(t, dir, "list-secrets"); err != nil || !strings.Contains(out, "ike") {
		t.Errorf("list-secrets in a project that lost its key: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(key); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a reading command made a key: %v", statErr)
	}
	// The key restored, the same command works.
	if err := os.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret-two\n", "add-credential", "web02", "--username", "u", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential after the key was restored: %v\n%s", err, out)
	}
}

// TestCLI_ANewProjectGetsItsKeyFromItsFirstWriter: in a project holding
// nothing sealed, each writer, run first, makes the master key and stores what
// it was given, exactly as before; a new user's first command still works.
func TestCLI_ANewProjectGetsItsKeyFromItsFirstWriter(t *testing.T) {
	unsetMasterKeyEnv(t)
	for _, w := range keyWriters(t) {
		if !w.firstUse {
			continue
		}
		t.Run(w.name, func(t *testing.T) {
			dir := t.TempDir()
			if out, err := runPleiades(t, dir, "init"); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			if out, err := runPleiadesWithStdin(t, dir, w.stdin, w.args...); err != nil {
				t.Fatalf("%s as a new project's first command: %v\n%s", w.name, err, out)
			}
			info, err := os.Stat(filepath.Join(dir, ".pleiades", "master.key"))
			if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
				t.Fatalf("%s made no owner-only key: %v", w.name, err)
			}
		})
	}
}

// TestCLI_ARunInAProjectThatLostItsKeyMakesNone: a run in a project that lost
// its master key refuses before any device is contacted, naming what is
// sealed, and leaves no new key: through the named secrets it masks from the
// start, and, in a project with no named secret, through the credential its
// task looks up.
func TestCLI_ARunInAProjectThatLostItsKeyMakesNone(t *testing.T) {
	unsetMasterKeyEnv(t)
	for name, tc := range map[string]struct {
		secret bool
		names  string
	}{
		"named secrets masked from the start": {true, "credentials.yaml (1 device credential, 1 named secret)"},
		"a task's credential lookup":          {false, "credentials.yaml (1 device credential)"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if out, err := runPleiades(t, dir, "init"); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			// 127.0.0.1 port 1: nothing listens there, so a run that got as far
			// as dialing would fail on that, not on the key.
			if out, err := runPleiades(t, dir, "add-host", "web01", "--type", "linux_server", "--set", "host=127.0.0.1", "--set", "port=1"); err != nil {
				t.Fatalf("add-host: %v\n%s", err, out)
			}
			if out, err := runPleiadesWithStdin(t, dir, "s3cret-one\n", "add-credential", "web01", "--username", "u", "--password-stdin"); err != nil {
				t.Fatalf("add-credential: %v\n%s", err, out)
			}
			if tc.secret {
				if out, err := runPleiadesWithStdin(t, dir, "s3cret-psk\n", "add-secret", "ike", "--field", "psk", "--value-stdin"); err != nil {
					t.Fatalf("add-secret: %v\n%s", err, out)
				}
			}
			runbook := "# Runs one command on web01.\n# Blast radius: web01, read only.\n# Changes: nothing.\n# Undo: nothing to undo.\n" +
				"id: lost-key\nhosts: web01\ntasks:\n  - name: look\n    ssh_exec:\n      command: uptime\n"
			if err := os.WriteFile(filepath.Join(dir, "runbooks", "lost.yaml"), []byte(runbook), 0o600); err != nil {
				t.Fatal(err)
			}
			key := filepath.Join(dir, ".pleiades", "master.key")
			if err := os.Remove(key); err != nil {
				t.Fatal(err)
			}
			out, err := runPleiades(t, dir, "run", filepath.Join("runbooks", "lost.yaml"))
			if err == nil || !strings.Contains(out, tc.names) || !strings.Contains(out, "no new key is made") {
				t.Fatalf("run in a project that lost its key: %v, want a refusal naming %q:\n%s", err, tc.names, out)
			}
			if strings.Contains(out, "s3cret") {
				t.Errorf("the refusal shows a secret:\n%s", out)
			}
			if _, statErr := os.Stat(key); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("the run made a new key: %v", statErr)
			}
		})
	}
}

// vaultSnapshot is every file under dir's .pleiades with its bytes.
func vaultSnapshot(t *testing.T, dir string) []byte {
	t.Helper()
	var out bytes.Buffer
	err := filepath.WalkDir(filepath.Join(dir, ".pleiades"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		out.WriteString(path + "\x00")
		out.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestCLI_WritersStartedTogetherInANewProjectAgreeOnOneKey: several
// add-credential processes started at once in a project with no key all
// succeed, each sealing its credential, every one of them is stored, and each
// opens with the one key file left behind. A process that found no key, then found a
// credential another had just sealed under the key it made, once refused the
// project as one whose key was lost.
func TestCLI_WritersStartedTogetherInANewProjectAgreeOnOneKey(t *testing.T) {
	unsetMasterKeyEnv(t)
	const writers = 12
	for round := range 4 {
		dir := t.TempDir()
		if out, err := runPleiades(t, dir, "init"); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		cmds := make([]*exec.Cmd, writers)
		outs := make([]*bytes.Buffer, writers)
		for i := range writers {
			cmds[i] = exec.Command(binPath, "add-credential", fmt.Sprintf("web%02d", i), "--username", "u", "--password-stdin", "--no-device")
			cmds[i].Dir = dir
			cmds[i].Stdin = strings.NewReader("s3cret-pw\n")
			outs[i] = &bytes.Buffer{}
			cmds[i].Stdout, cmds[i].Stderr = outs[i], outs[i]
		}
		for _, c := range cmds {
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
		}
		for i, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Fatalf("round %d, writer %d: %v\n%s", round, i, err, outs[i])
			}
		}
		key, err := credential.ExistingMasterKey(dir)
		if err != nil {
			t.Fatal(err)
		}
		store, err := credential.NewFileStore(dir, key)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := credential.ListCredentials(dir)
		// Every writer's credential is there: each save holds the credential
		// file lock across its read and its write, so none writes the file
		// back without another's entry.
		if err != nil || len(listed) != writers {
			t.Fatalf("round %d: %d writers left %d credentials: %v", round, writers, len(listed), err)
		}
		for _, c := range listed {
			if got, err := store.Lookup(context.Background(), c.Name); err != nil || got.Password != "s3cret-pw" {
				t.Errorf("round %d: %s does not open with the key left behind: %v", round, c.Name, err)
			}
		}
	}
}
