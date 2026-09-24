// Benchmarks for SCP transfers, compared with OpenSSH's own scp in legacy
// mode against the same server.
package scpxfer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/scpxfer"
)

// benchSetup prepares the shared server for the transfer benchmarks, as
// pkg/sftpxfer's own does.
func benchSetup(b *testing.B) (*testsupport.SSHD, filexfertest.CLI, filexfer.Path, filexfer.Path) {
	sshd := requireSSHD(b)
	ctx := context.Background()
	root := testsupport.SSHDHome + "/bench"
	if _, err := sshd.RootExec(ctx, "rm -rf "+root+" && mkdir -p "+root+
		" && head -c 67108864 /dev/urandom > "+root+"/get.bin && chown -R 1000:1000 "+root); err != nil {
		b.Fatal(err)
	}
	key, err := sshd.InstallClientKey(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	put, _ := filexfer.Resolve(root, "put.bin")
	get, _ := filexfer.Resolve(root, "get.bin")
	cli := filexfertest.CLI{Host: sshd.Host, Port: sshd.Port, User: testsupport.SSHDUser, KeyPath: key, KnownHosts: sshdKnownHosts}
	return sshd, cli, put, get
}

// benchOpen dials a fresh verified connection for each operation.
func benchOpen(b *testing.B, sshd *testsupport.SSHD) func() (filexfer.Store, func()) {
	return func() (filexfer.Store, func()) {
		runner := remoteexec.New(remoteexec.Options{KnownHostsPath: sshdKnownHosts})
		auth := remoteexec.PasswordAuth(testsupport.SSHDUser, testsupport.SSHDPassword)
		conn, err := runner.Connect(context.Background(), nil, remoteexec.Target{Host: sshd.Host, Port: sshd.Port}, auth)
		if err != nil {
			b.Fatal(err)
		}
		return scpxfer.New(conn), func() { _ = conn.Close() }
	}
}

// BenchmarkSCPPut and BenchmarkSCPGet measure this package, one fresh
// connection and one 64 MiB transfer per operation, including the
// containment preflight and the atomic rename.
func BenchmarkSCPPut(b *testing.B) {
	sshd, _, put, _ := benchSetup(b)
	filexfertest.BenchPut(b, benchOpen(b, sshd), put)
}

// BenchmarkSCPGet is BenchmarkSCPPut in the other direction.
func BenchmarkSCPGet(b *testing.B) {
	sshd, _, _, get := benchSetup(b)
	filexfertest.BenchGet(b, benchOpen(b, sshd), get)
}

// BenchmarkOpenSSHSCPClientPut and BenchmarkOpenSSHSCPClientGet are the
// comparison: OpenSSH's own scp in legacy mode (-O), the same protocol,
// moving the same 64 MiB against the same server, with none of this
// package's checks.
func BenchmarkOpenSSHSCPClientPut(b *testing.B) {
	_, cli, put, _ := benchSetup(b)
	local := filepath.Join(b.TempDir(), "put.bin")
	if err := os.WriteFile(local, make([]byte, filexfertest.BenchSize), 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(filexfertest.BenchSize)
	b.ResetTimer()
	for b.Loop() {
		if err := cli.SCP(local, cli.Remote(put.String())); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenSSHSCPClientGet is the comparison's other direction.
func BenchmarkOpenSSHSCPClientGet(b *testing.B) {
	_, cli, _, get := benchSetup(b)
	local := filepath.Join(b.TempDir(), "got.bin")
	b.SetBytes(filexfertest.BenchSize)
	b.ResetTimer()
	for b.Loop() {
		if err := cli.SCP(cli.Remote(get.String()), local); err != nil {
			b.Fatal(err)
		}
	}
}
