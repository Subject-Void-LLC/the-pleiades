// Benchmarks for SFTP transfers, compared with OpenSSH's own sftp client
// against the same server.
package sftpxfer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sftpxfer"
)

// benchSetup prepares the shared server for the transfer benchmarks: a
// root holding a BenchSize file to get, a client key the OpenSSH
// command-line client can use, and the Path both sides transfer.
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

// benchOpen dials a fresh verified connection and SFTP session.
func benchOpen(b *testing.B, sshd *testsupport.SSHD) func() (filexfer.Store, func()) {
	return func() (filexfer.Store, func()) {
		ctx := context.Background()
		runner := remoteexec.New(remoteexec.Options{KnownHostsPath: sshdKnownHosts})
		auth := remoteexec.PasswordAuth(testsupport.SSHDUser, testsupport.SSHDPassword)
		conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: sshd.Host, Port: sshd.Port}, auth)
		if err != nil {
			b.Fatal(err)
		}
		sub, err := conn.Subsystem(ctx, "sftp")
		if err != nil {
			b.Fatal(err)
		}
		client, err := sftpxfer.Open(ctx, sub)
		if err != nil {
			b.Fatal(err)
		}
		return client, func() { _ = client.Close(); _ = conn.Close() }
	}
}

// BenchmarkSFTPPut and BenchmarkSFTPGet measure this package, one fresh
// connection and one 64 MiB transfer per operation.
func BenchmarkSFTPPut(b *testing.B) {
	sshd, _, put, _ := benchSetup(b)
	filexfertest.BenchPut(b, benchOpen(b, sshd), put)
}

// BenchmarkSFTPGet is BenchmarkSFTPPut in the other direction.
func BenchmarkSFTPGet(b *testing.B) {
	sshd, _, _, get := benchSetup(b)
	filexfertest.BenchGet(b, benchOpen(b, sshd), get)
}

// BenchmarkOpenSSHSFTPClientPut and BenchmarkOpenSSHSFTPClientGet are the
// comparison: the OpenSSH sftp command-line client moving the same
// 64 MiB against the same server, which is also what Ansible's copy and
// fetch modules drive underneath.
func BenchmarkOpenSSHSFTPClientPut(b *testing.B) {
	_, cli, put, _ := benchSetup(b)
	local := filepath.Join(b.TempDir(), "put.bin")
	if err := os.WriteFile(local, make([]byte, filexfertest.BenchSize), 0o600); err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	b.SetBytes(filexfertest.BenchSize)
	b.ResetTimer()
	for b.Loop() {
		if err := cli.SFTP(dir, "put "+local+" "+put.String()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenSSHSFTPClientGet is the comparison's other direction.
func BenchmarkOpenSSHSFTPClientGet(b *testing.B) {
	_, cli, _, get := benchSetup(b)
	dir := b.TempDir()
	local := filepath.Join(dir, "got.bin")
	b.SetBytes(filexfertest.BenchSize)
	b.ResetTimer()
	for b.Loop() {
		if err := cli.SFTP(dir, "get "+get.String()+" "+local); err != nil {
			b.Fatal(err)
		}
	}
}
