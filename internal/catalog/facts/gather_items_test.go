// facts.gather's files, ports and users subsets against THIS machine,
// through the real SSH harness and a real /bin/sh (Phase 41c): files the test
// makes, a socket it listens on, and the accounts in /etc/passwd, each held
// to what this test reads with code that shares nothing with the method.
package facts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// gatherNeedsLinux skips a test whose expectations are read from Linux's
// own files; the lab gates hold the other platforms.
func gatherNeedsLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("reads this machine's /proc and /etc as Linux lays them out, and this is %s", runtime.GOOS)
	}
}

// gatherItem is one family member's value as an object, failing t when it
// is missing.
func gatherItem(t *testing.T, rc *gatherContext, key string) map[string]any {
	t.Helper()
	v, ok := rc.facts[key].(map[string]any)
	if !ok {
		t.Fatalf("no fact %s (have %d facts)", key, len(rc.facts))
	}
	return v
}

// gatherItemKey is the fact key of family's member name, as the method
// encodes it: a slash, a percent sign and anything unprintable or a space,
// byte by byte, as %XX.
func gatherItemKey(family, name string) string {
	var b strings.Builder
	b.WriteString(family)
	for _, c := range []byte(name) {
		if c == '/' || c == '%' || c <= ' ' || c >= 0x7f {
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// gatherFileKey is the fact key of path.
func gatherFileKey(path string) string { return gatherItemKey("file.", path) }

func TestGather_FilesReportDigestsAndNeverCallUnreadableAbsent(t *testing.T) {
	gatherNeedsLinux(t)
	dir := t.TempDir()
	// A name that would break any quoting short of the real thing.
	plain := filepath.Join(dir, `it's $(touch pwned) "x"`)
	if err := os.WriteFile(plain, []byte("hello facts\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target with\nnewline", link); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(locked, "inside")
	if err := os.WriteFile(hidden, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	gone := filepath.Join(dir, "gone")
	// A file anyone may read, under directories anyone may search, is the
	// only kind whose digest is taken. t.TempDir's own directories are
	// private, so they are opened up for it.
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(dir, "public")
	if err := os.WriteFile(public, []byte("hello facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Readable by this login, and by nobody else: its directory is private.
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	inPrivate := filepath.Join(private, "public-mode")
	if err := os.WriteFile(inPrivate, []byte("s3cret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{
		"gather_subset": []any{"!all", "!min"},
		"files":         []any{plain, link, hidden, gone, dir, public, inPrivate},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a path's $(...) ran on the device")
	}
	sum := sha256.Sum256([]byte("hello facts\n"))
	pub := gatherItem(t, rc, gatherFileKey(public))
	if pub["sha256"] != hex.EncodeToString(sum[:]) || pub["readable"] != true || pub["world_readable"] != true || pub["mode"] != "0644" ||
		pub["type"] != "file" || pub["uid"] != os.Getuid() || int64Of(pub["size"]) != 12 {
		t.Errorf("%s = %v", public, pub)
	}
	// Neither a file only its group may read nor one in a private
	// directory keeps a plain digest or its size, though this login reads
	// both: its digest is handed over only under the raw field, which the
	// engine keys or seals before anything keeps it.
	for p, content := range map[string]string{plain: "hello facts\n", inPrivate: "s3cret\n"} {
		f := gatherItem(t, rc, gatherFileKey(p))
		raw := sha256.Sum256([]byte(content))
		if f["sha256_raw"] != hex.EncodeToString(raw[:]) || f["world_readable"] != false || f["sha256"] != nil || f["size"] != nil || f["state"] != "present" {
			t.Errorf("%s = %v, want its digest raw for the engine and no size", p, f)
		}
	}
	f := gatherItem(t, rc, gatherFileKey(plain))
	if f["mode"] != "0640" {
		t.Errorf("%s = %v", plain, f)
	}
	if _, ok := f["content"]; ok {
		t.Error("a file fact carried content")
	}
	if l := gatherItem(t, rc, gatherFileKey(link)); l["type"] != "link" || l["link_target"] != "target with\nnewline" || l["sha256"] != nil {
		t.Errorf("link = %v", l)
	}
	if d := gatherItem(t, rc, gatherFileKey(dir)); d["type"] != "directory" || d["size"] != nil {
		t.Errorf("directory = %v", d)
	}
	if g := gatherItem(t, rc, gatherFileKey(gone)); g["state"] != "absent" {
		t.Errorf("a missing file = %v, want absent", g)
	}
	// The case this subset exists to get right: a file in a directory this
	// login may not search exists, and is not reported absent. Root may
	// search anything, so it reads the file instead.
	h := gatherItem(t, rc, gatherFileKey(hidden))
	if os.Geteuid() == 0 {
		if h["state"] != "present" {
			t.Errorf("as root, %s = %v", hidden, h)
		}
	} else if h["state"] != "unreadable" {
		t.Errorf("a file in a directory this login cannot search = %v, want unreadable, never absent", h)
	}
	for k := range rc.facts {
		if !strings.HasPrefix(k, "file.") {
			t.Errorf("subsets !all and !min still gathered %s", k)
		}
	}
}

// int64Of is a size fact as an int64.
func int64Of(v any) int64 {
	n, _ := v.(int64)
	return n
}

func TestGather_PortsFindsAListenerAndItsProcess(t *testing.T) {
	gatherNeedsLinux(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	comm, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Skipf("no /proc/self/comm: %v", err)
	}

	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"ports"}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": ln.Addr().(*net.TCPAddr).Port,
		"uid": os.Getuid(), "process": strings.TrimSpace(string(comm))}
	key := "port.tcp:127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if got := gatherItem(t, rc, key); !equalMaps(got, want) {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
	ukey := "port.udp:127.0.0.1:" + strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port)
	if got := gatherItem(t, rc, ukey); got["process"] != want["process"] {
		t.Errorf("%s = %v", ukey, got)
	}
	// The harness's own SSH listener is this process's too.
	skey := "port.tcp:" + server.host + ":" + strconv.Itoa(server.port)
	if strings.Contains(server.host, ":") {
		skey = "port.tcp:[" + server.host + "]:" + strconv.Itoa(server.port)
	}
	if _, ok := rc.facts[skey]; !ok {
		t.Errorf("no fact for the SSH server's own listener %s", skey)
	}
}

// equalMaps compares two fact objects by their printed values, since a
// number may be an int on one side and another integer type on the other.
func equalMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if strconv.Quote(strings.TrimSpace(toString(v))) != strconv.Quote(strings.TrimSpace(toString(b[k]))) {
			return false
		}
	}
	return true
}

// toString prints a fact value's scalar.
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	}
	return "?"
}

func TestGather_UsersMatchThisMachinesFilesAndCarryNoPassword(t *testing.T) {
	gatherNeedsLinux(t)
	passwd := gatherReadFile(t, "/etc/passwd")
	group := gatherReadFile(t, "/etc/group")
	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"users"}}); err != nil {
		t.Fatal(err)
	}
	accounts := 0
	for _, line := range strings.Split(passwd, "\n") {
		f := strings.Split(line, ":")
		if len(f) != 7 || f[0] == "" || strings.ContainsAny(f[0][:1], "#+-") {
			continue
		}
		accounts++
		u := gatherItem(t, rc, gatherItemKey("user.", f[0]))
		if toString(u["uid"]) != f[2] || toString(u["gid"]) != f[3] || u["home"] != f[5] || u["shell"] != f[6] || u["source"] != "files" {
			t.Errorf("user %s = %v, /etc/passwd says %v", f[0], u, f)
		}
	}
	groups := 0
	for _, line := range strings.Split(group, "\n") {
		f := strings.Split(line, ":")
		if len(f) != 4 || f[0] == "" || strings.ContainsAny(f[0][:1], "#+-") {
			continue
		}
		groups++
		g := gatherItem(t, rc, gatherItemKey("group.", f[0]))
		members, _ := g["members"].([]string)
		if toString(g["gid"]) != f[2] || strings.Join(members, ",") != strings.ReplaceAll(f[3], " ", "") {
			t.Errorf("group %s = %v, /etc/group says %v", f[0], g, f)
		}
	}
	if accounts == 0 || groups == 0 {
		t.Fatalf("this machine's files list %d accounts and %d groups", accounts, groups)
	}
	if got := len(rc.facts); got != accounts+groups+len(gatherMinKeys(rc)) {
		t.Errorf("%d facts for %d accounts and %d groups", got, accounts, groups)
	}
}

// gatherMinKeys is the min subset's facts among rc's, which the users
// subset is gathered beside.
func gatherMinKeys(rc *gatherContext) []string {
	var keys []string
	for k := range rc.facts {
		if strings.HasPrefix(k, "ansible_") {
			keys = append(keys, k)
		}
	}
	return keys
}

func TestGather_RefusesAPathWrittenForTheOtherKindOfSystem(t *testing.T) {
	gatherNeedsLinux(t)
	server := startGatherServer(t)
	rc := newGatherContext(server)
	_, err := gatherRun(t, server, rc, map[string]any{"files": []any{`C:\Windows\win.ini`}})
	if err == nil || !strings.Contains(err.Error(), "not an absolute path on Linux") {
		t.Fatalf("a Windows path on Linux: %v", err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("a refused task still emitted %d facts", len(rc.facts))
	}
}
