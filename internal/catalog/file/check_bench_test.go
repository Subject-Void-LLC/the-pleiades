// Package file_test: benchmarks of each file method's check against its real
// run, and ansible-playbook --check as the reference.
package file_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Phase 46's benchmark of a check against the real run it predicts, for
// the three file methods that can be checked, each on the in-process SSH
// harness (a real SSH server running every command through a real
// /bin/sh). The six svc.systemd.* methods are left out on purpose: this
// harness runs commands on the machine running the benchmark, and a
// benchmark that started and stopped that machine's own services would be
// the wrong kind of test. BenchmarkAnsibleCheck_FileMethods is the
// reference: ansible-playbook --check over the same three tasks.
//
// Each iteration starts from a fresh state that needs a change (a missing
// directory, a wrong mode, a file to remove), so the real run always does
// the work, and the check always has to find out that it would.

// benchServer starts the harness for b and returns where it listens.
func benchServer(b *testing.B) dirServer {
	b.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		b.Fatalf("starting the SSH harness: %v", err)
	}
	b.Cleanup(srv.Close)
	return dirServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// benchFileMethod runs method once per iteration against a fresh starting
// state setup builds, with the timer stopped while it is built.
func benchFileMethod(b *testing.B, method collection.Method, setup func(dir string) map[string]any) {
	server := benchServer(b)
	device := newDirDevice(server)
	root := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := filepath.Join(root, strconv.Itoa(i))
		if err := os.Mkdir(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		params := setup(dir)
		var rc sdk.RunbookContext = newDirContext(server)
		b.StartTimer()
		if _, err := method(context.Background(), rc, device, params); err != nil {
			b.Fatalf("iteration %d: %v", i, err)
		}
	}
}

// directorySetup is a missing directory to create with mode 0750.
func directorySetup(dir string) map[string]any {
	params := dirParams(filepath.Join(dir, "made"))
	params["mode"] = "0750"
	return params
}

// permissionsSetup is an existing file whose mode is wrong.
func permissionsSetup(dir string) map[string]any {
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		panic(err)
	}
	params := dirParams(path)
	params["mode"] = "0640"
	return params
}

// removeSetup is an existing file to remove.
func removeSetup(dir string) map[string]any {
	path := filepath.Join(dir, "gone")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		panic(err)
	}
	return dirParams(path)
}

func BenchmarkDirectory_Check(b *testing.B) {
	benchFileMethod(b, file.CheckDirectory, directorySetup)
}

func BenchmarkDirectory_Run(b *testing.B) { benchFileMethod(b, file.Directory, directorySetup) }

func BenchmarkPermissions_Check(b *testing.B) {
	benchFileMethod(b, file.CheckPermissions, permissionsSetup)
}

func BenchmarkPermissions_Run(b *testing.B) { benchFileMethod(b, file.Permissions, permissionsSetup) }

func BenchmarkRemove_Check(b *testing.B) { benchFileMethod(b, file.CheckRemove, removeSetup) }

func BenchmarkRemove_Run(b *testing.B) { benchFileMethod(b, file.Remove, removeSetup) }

// BenchmarkAnsibleCheck_FileMethods is the reference platform: the same
// three changes as one playbook, run by ansible-playbook --check against
// the local machine with no SSH at all, so Ansible's number is a floor on
// what it would cost over a connection. It is skipped, not failed, when
// ansible-playbook is not on PATH, like this repository's other reference
// benchmarks.
func BenchmarkAnsibleCheck_FileMethods(b *testing.B) {
	path, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH")
	}
	dir := b.TempDir()
	inventory := filepath.Join(dir, "inventory.ini")
	if err := os.WriteFile(inventory, []byte("localhost ansible_connection=local\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	existing := filepath.Join(dir, "file")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		b.Fatal(err)
	}
	playbook := filepath.Join(dir, "check.yml")
	content := "---\n- hosts: all\n  gather_facts: false\n  tasks:\n" +
		"    - file: {path: " + filepath.Join(dir, "made") + ", state: directory, mode: \"0750\"}\n" +
		"    - file: {path: " + existing + ", mode: \"0640\"}\n" +
		"    - file: {path: " + existing + ", state: absent}\n"
	if err := os.WriteFile(playbook, []byte(content), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		cmd := exec.Command(path, "--check", "-i", inventory, playbook)
		cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook --check failed: %v\n%s", err, out)
		}
	}
}
