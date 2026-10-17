// Tests for HostFiles: one suite run against this machine's own files and
// against a real OpenSSH server's, through the real remoteexec login.
package containerengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// sha is content's digest as HostFiles reports one.
func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// exerciseHostFiles runs every HostFiles operation against dir, an empty
// directory h reaches, reading modes through modeOf and listing a
// directory's names through list.
func exerciseHostFiles(t *testing.T, h *HostFiles, dir string, modeOf func(string) string, list func(string) []string) {
	t.Helper()
	ctx := context.Background()
	if home, err := h.Home(ctx); err != nil || !strings.HasPrefix(home, "/") {
		t.Errorf("Home = %q, %v", home, err)
	}

	config := dir + "/sub/config.json"
	if _, ok, err := h.ReadSmall(ctx, config, 100); ok || err != nil {
		t.Errorf("ReadSmall of an absent file = %v, %v", ok, err)
	}
	if err := h.WritePrivate(ctx, config, []byte(`{"auths":{}}`)); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(config); m != "0600" {
		t.Errorf("the written file's mode = %s, want 0600", m)
	}
	if m := modeOf(dir + "/sub"); m != "0700" {
		t.Errorf("the made directory's mode = %s, want 0700", m)
	}
	if data, ok, err := h.ReadSmall(ctx, config, 100); !ok || err != nil || string(data) != `{"auths":{}}` {
		t.Errorf("ReadSmall = %q, %v, %v", data, ok, err)
	}
	if _, _, err := h.ReadSmall(ctx, config, 4); err == nil {
		t.Error("ReadSmall over its bound succeeded")
	}
	if d, ok, err := h.Digest(ctx, config); !ok || err != nil || d != sha(`{"auths":{}}`) {
		t.Errorf("Digest = %q, %v, %v", d, ok, err)
	}

	archive := dir + "/a.tar"
	for i, step := range []struct {
		content string
		changed bool
	}{{"first archive", true}, {"first archive", false}, {"second archive", true}} {
		got, err := h.Store(ctx, archive, strings.NewReader(step.content))
		if err != nil {
			t.Fatalf("Store %d: %v", i, err)
		}
		if got.Changed != step.changed || got.Digest != sha(step.content) || got.Size != int64(len(step.content)) {
			t.Errorf("Store %d = %+v, want changed %v digest %s", i, got, step.changed, sha(step.content))
		}
	}
	if m := modeOf(archive); m != "0600" {
		t.Errorf("the archive's mode = %s, want 0600", m)
	}
	for _, name := range list(dir) {
		if strings.HasPrefix(name, ".pleiades") {
			t.Errorf("a temporary was left behind: %s", name)
		}
	}
	r, err := h.Open(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(data) != "second archive" {
		t.Errorf("Open read %q, %v", data, err)
	}
	if _, err := h.Open(ctx, dir+"/absent.tar"); !errors.Is(err, ErrHostFileAbsent) {
		t.Errorf("Open of an absent file = %v", err)
	}
	if _, err := h.Open(ctx, dir+"/sub"); err == nil {
		t.Error("Open of a directory succeeded")
	}
	if _, err := h.Store(ctx, dir+"/sub", strings.NewReader("x")); err == nil {
		t.Error("Store over a directory succeeded")
	}
	if _, err := h.Store(ctx, dir+"/missing/a.tar", strings.NewReader("x")); err == nil {
		t.Error("Store into a missing directory succeeded")
	}
	if removed, err := h.Remove(ctx, archive); !removed || err != nil {
		t.Errorf("Remove = %v, %v", removed, err)
	}
	if removed, err := h.Remove(ctx, archive); removed || err != nil {
		t.Errorf("Remove of an absent file = %v, %v", removed, err)
	}
	for _, bad := range []string{"relative", dir + "/../x", "/", ""} {
		if _, err := h.Store(ctx, bad, strings.NewReader("x")); err == nil {
			t.Errorf("Store(%q) succeeded", bad)
		}
	}
}

// TestHostFiles_Local runs the suite against this machine's own files.
func TestHostFiles_Local(t *testing.T) {
	dir := t.TempDir()
	modeOf := func(p string) string {
		info, err := os.Stat(p)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("%04o", info.Mode().Perm())
	}
	list := func(d string) []string {
		entries, _ := os.ReadDir(d)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	exerciseHostFiles(t, &HostFiles{fs: localFS{}, Local: true}, dir, modeOf, list)
}

// TestHostFiles_OverSSH runs the suite against a real OpenSSH server's
// files, through the remoteexec login a session's carriers ride.
func TestHostFiles_OverSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real sshd container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sshd, err := testsupport.StartSSHD(ctx)
	testsupport.Require(t, "docker", err == nil, fmt.Sprintf("could not start the sshd container: %v", err))
	t.Cleanup(func() { _ = sshd.Terminate(context.Background()) })
	known, err := sshd.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: known})
	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: sshd.Host, Port: sshd.Port},
		remoteexec.PasswordAuth(testsupport.SSHDUser, testsupport.SSHDPassword))
	if err != nil {
		t.Fatal(err)
	}
	h := &HostFiles{fs: &sshFS{conn: conn}}
	t.Cleanup(h.Close)
	dir := testsupport.SSHDHome + "/hostfiles-test"
	if res, err := conn.Run(ctx, "mkdir -p "+dir); err != nil || res.ExitCode != 0 {
		t.Fatalf("mkdir = %+v, %v", res, err)
	}
	modeOf := func(p string) string {
		info, err := remotefile.Stat(ctx, conn, p)
		if err != nil {
			return err.Error()
		}
		return info.Mode
	}
	list := func(d string) []string {
		res, err := conn.Run(ctx, "ls -A "+remoteexec.QuoteArg(d))
		if err != nil {
			return nil
		}
		return strings.Fields(res.Stdout)
	}
	exerciseHostFiles(t, h, dir, modeOf, list)
	if home, err := h.Home(ctx); err != nil || home != testsupport.SSHDHome {
		t.Errorf("Home = %q, %v; want %s", home, err, testsupport.SSHDHome)
	}
}

// TestOpenHostFiles_LocalFollowsThePolicy: a device reached through this
// machine's socket gets this machine's files only where the local carrier
// is allowed, as a session does; no device is refused.
func TestOpenHostFiles_LocalFollowsThePolicy(t *testing.T) {
	prev := localCarrier.Load()
	t.Cleanup(func() { localCarrier.Store(prev) })
	h := &engineHost{caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal},
		engines: []capability.EngineKind{capability.EngineDocker}, socket: "/s", carriers: []capability.Carrier{capability.CarrierLocal}}

	localCarrier.Store(false)
	if _, err := OpenHostFiles(context.Background(), nil, h, nil, "x.y"); err == nil || !strings.Contains(err.Error(), "engine_local") {
		t.Errorf("OpenHostFiles without the local carrier = %v", err)
	}
	localCarrier.Store(true)
	files, err := OpenHostFiles(context.Background(), nil, h, nil, "x.y")
	if err != nil || !files.Local {
		t.Fatalf("OpenHostFiles = %+v, %v", files, err)
	}
	files.Close()
	if _, err := OpenHostFiles(context.Background(), nil, nil, nil, "x.y"); err == nil {
		t.Error("OpenHostFiles with no device succeeded")
	}
}

// TestValidHostPath refuses every path that is not absolute and clean.
func TestValidHostPath(t *testing.T) {
	for _, ok := range []string{"/a", "/var/lib/x.tar", "/a b/c"} {
		if err := ValidHostPath("path", ok); err != nil {
			t.Errorf("ValidHostPath(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "./a", "/a/", "/a/../b", "/a//b", "/", "/a\nb", "/a\x00b"} {
		if err := ValidHostPath("path", bad); err == nil {
			t.Errorf("ValidHostPath(%q) was accepted", bad)
		}
	}
}
