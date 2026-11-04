// The Crawl tier's Release Gate for persisted facts (Phase 41, 41b): a real
// run of a method that gathers facts, against a real sshd container through
// the real binary, stores them under the project's .pleiades/facts; pleiades
// facts reads them back as the device reports them, read beside the method,
// each with its declared kind and the run that saw it, ordered by the
// project's run counter; a second gather writes no new value but confirms
// the old one, and the volatile uptime keeps one value; a check stores
// nothing; and --verify passes the chain and fails it once a stored value is
// edited.
package main_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// factsView is pleiades facts --json.
type factsView struct {
	Host     string `json:"host"`
	DeviceID string `json:"device_id"`
	Key      string `json:"key"`
	Facts    []struct {
		Key        string      `json:"key"`
		Value      any         `json:"value"`
		Hash       string      `json:"hash"`
		ObservedAt time.Time   `json:"observed_at"`
		LastSeenAt time.Time   `json:"last_seen_at"`
		Kind       string      `json:"kind"`
		By         factsByView `json:"by"`
		LastBy     factsByView `json:"last_by"`
	} `json:"facts"`
}

// factsByView is a stored value's provenance as pleiades facts --json prints
// it.
type factsByView struct {
	Run    string `json:"run"`
	Node   string `json:"node"`
	Method string `json:"method"`
	Order  struct {
		Source string `json:"source"`
		Token  uint64 `json:"token"`
	} `json:"order"`
}

func TestCLI_FactsReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the facts Release Gate container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)
	g := newRollbackGate(t, container, host, port)
	read := func(args ...string) factsView {
		t.Helper()
		out, code := g.pleiades(t, append([]string{"facts", "container1"}, append(args, "--json")...)...)
		var v factsView
		if code != 0 || json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &v) != nil {
			t.Fatalf("pleiades facts %v: exit %d\n%s", args, code, out)
		}
		return v
	}
	gather := func(mode ...string) {
		t.Helper()
		if out, code := g.pleiades(t, append([]string{"adhoc", "container1", "facts.gather"}, mode...)...); code != 0 {
			t.Fatalf("facts.gather %v: exit %d\n%s", mode, code, out)
		}
	}

	if v := read(); len(v.Facts) != 0 {
		t.Fatalf("a host no run has gathered from has facts: %+v", v.Facts)
	}
	gather()
	first := read()
	kernel := ""
	for _, f := range first.Facts {
		if f.Key == "ansible_kernel" {
			kernel, _ = f.Value.(string)
		}
	}
	if want := g.device(t, "uname -r"); kernel != want || first.DeviceID == "" {
		t.Fatalf("stored ansible_kernel %q for device %q, the device says %q", kernel, first.DeviceID, want)
	}
	kinds := map[string]string{}
	for _, f := range first.Facts {
		kinds[f.Key] = f.Kind
		if f.By.Run == "" || f.By.Node == "" || f.By.Method != "facts.gather" || f.By.Order.Source != "clock" || f.By.Order.Token == 0 {
			t.Errorf("%s: provenance %+v, want this run, its node, facts.gather and the run counter", f.Key, f.By)
		}
	}
	if kinds["ansible_kernel"] != "stable" || kinds["ansible_uptime_seconds"] != "volatile" {
		t.Errorf("kinds = %v, want the kernel stable and the uptime volatile as declared", kinds)
	}
	history := filepath.Join(g.dir, ".pleiades", "facts", first.DeviceID+".jsonl")
	lines := func() int {
		raw, err := os.ReadFile(history) // #nosec G304 -- the test's own project
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "\n")
	}
	written := lines()
	if written == 0 {
		t.Fatal("the gather wrote no history")
	}

	gather()
	second := read()
	// The uptime moved, but it is volatile: no history line for it.
	if got := lines(); got != written {
		t.Errorf("a second gather of an unchanged device wrote %d history lines", got-written)
	}
	for _, f := range second.Facts {
		if f.Key == "ansible_kernel" {
			if !f.LastSeenAt.After(f.ObservedAt) || f.LastBy.Order.Token <= f.By.Order.Token || f.LastBy.Run == f.By.Run {
				t.Errorf("the kernel, gathered again unchanged, was not confirmed by the second run: %+v", f)
			}
		}
	}
	if h := read("ansible_kernel"); len(h.Facts) != 1 {
		t.Errorf("the kernel's history has %d values, want 1", len(h.Facts))
	}
	if h := read("ansible_uptime_seconds"); len(h.Facts) != 1 || h.Facts[0].Kind != "volatile" {
		t.Errorf("the uptime's history = %+v, want its one latest value", h.Facts)
	}

	before := lines()
	gather("--mode", "check")
	if got := lines(); got != before {
		t.Errorf("a check stored %d lines", got-before)
	}
	if out, code := g.pleiades(t, "facts", "nosuchhost"); code == 0 {
		t.Errorf("facts of a host not in the inventory: exit 0\n%s", out)
	}

	// Phase 41c: the packages subset reports every package the device's own
	// database lists (this sshd image is Alpine, so apk), each with apk's
	// version, stored one fact each and readable by key.
	gather("gather_subset:=[packages, services]")
	all := read()
	packages := map[string]string{}
	for _, f := range all.Facts {
		if strings.HasPrefix(f.Key, "package.") {
			v, _ := f.Value.(map[string]any)
			version, _ := v["version"].(string)
			packages[f.Key] = version
			if v["source"] != "apk" || f.Kind != "stable" {
				t.Errorf("%s = %v (%s), want an apk package, stable", f.Key, v, f.Kind)
			}
		}
	}
	listed := strings.TrimSpace(g.device(t, "apk list --installed 2>/dev/null | grep -c installed"))
	if strconv.Itoa(len(packages)) != listed || len(packages) == 0 {
		t.Errorf("%d package facts stored; apk lists %s installed", len(packages), listed)
	}
	busybox := strings.TrimSpace(g.device(t, "apk list --installed busybox 2>/dev/null | awk '{print $1}'"))
	if want := strings.TrimPrefix(busybox, "busybox-"); packages["package.busybox"] != want {
		t.Errorf("package.busybox = %q; apk says %q", packages["package.busybox"], want)
	}
	if h := read("package.busybox"); len(h.Facts) != 1 {
		t.Errorf("package.busybox's history has %d values, want 1", len(h.Facts))
	}

	// Phase 41c-2: ports, users and named files, stored one fact each. The
	// login is not root: sshd's listener is root's, so its uid is known and
	// its process is not; a file in a directory only root may search is
	// unreadable, never absent; and the digest is the device's own.
	asRoot(t, g.container, "mkdir -p /rootonly && echo s > /rootonly/secret && chmod 700 /rootonly")
	// A listener of root's, whose process this login may not look into.
	asRoot(t, g.container, "(busybox nc -lk -p 7070 -e /bin/true >/dev/null 2>&1 &); sleep 1")
	gather("gather_subset:=[ports, users]", "files:=[/etc/hostname, /rootonly/secret, /nonexistent]")
	stored := map[string]map[string]any{}
	for _, f := range read().Facts {
		if v, ok := f.Value.(map[string]any); ok {
			stored[f.Key] = v
		}
	}
	// This image runs sshd (sshd.pam) as the login's own account, on 2222,
	// so its process is visible: the uid and name are the device's, read as
	// root from /proc.
	sshdUID := asRoot(t, g.container, `for p in /proc/[0-9]*; do [ "$(cat $p/comm 2>/dev/null)" = sshd.pam ] && stat -c %u $p && break; done`)
	for _, key := range []string{"port.tcp:0.0.0.0:2222", "port.tcp:[::]:2222"} {
		if sshd := stored[key]; sshd == nil || strconv.Itoa(int(numberOf(sshd["uid"]))) != sshdUID || sshd["process"] != "sshd.pam" {
			t.Errorf("%s = %v, want sshd.pam's listener, uid %s", key, sshd, sshdUID)
		}
	}
	if nc := stored["port.tcp:[::]:7070"]; nc == nil || nc["uid"] != float64(0) || nc["process"] != nil {
		t.Errorf("root's listener = %v, want uid 0 and no process this login may see", nc)
	}
	accounts := asRoot(t, g.container, "grep -cv '^#' /etc/passwd")
	users := 0
	for k, v := range stored {
		if strings.HasPrefix(k, "user.") {
			users++
			if _, ok := v["password"]; ok {
				t.Errorf("%s carries a password field", k)
			}
		}
	}
	if strconv.Itoa(users) != accounts {
		t.Errorf("%d user facts; /etc/passwd lists %s accounts", users, accounts)
	}
	if login := stored["user."+releaseGateSSHUser]; login == nil || login["home"] == "" {
		t.Errorf("user.%s = %v", releaseGateSSHUser, login)
	}
	digest := strings.Fields(asRoot(t, g.container, "sha256sum /etc/hostname"))[0]
	if h := stored["file.%2Fetc%2Fhostname"]; h == nil || h["sha256"] != digest || h["state"] != "present" || h["type"] != "file" {
		t.Errorf("file /etc/hostname = %v, the device's own digest is %s", h, digest)
	}
	if s := stored["file.%2Frootonly%2Fsecret"]; s == nil || s["state"] != "unreadable" {
		t.Errorf("a file in a directory only root may search = %v, want unreadable", s)
	}
	if n := stored["file.%2Fnonexistent"]; n == nil || n["state"] != "absent" {
		t.Errorf("a missing file = %v, want absent", n)
	}

	// The chain verifies; an edited value breaks it, and the command says so
	// by its exit status.
	if out, code := g.pleiades(t, "facts", "container1", "--verify"); code != 0 || !strings.Contains(out, "unbroken") {
		t.Fatalf("--verify of an untouched store: exit %d\n%s", code, out)
	}
	raw, err := os.ReadFile(history) // #nosec G304 -- the test's own project
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"`+kernel+`"`, `"0.0.0-edited"`, 1)
	if edited == string(raw) {
		t.Fatal("the kernel's value is not in the history file to edit")
	}
	if err := os.WriteFile(history, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := g.pleiades(t, "facts", "container1", "--verify"); code == 0 || !strings.Contains(out, "breaks at value") {
		t.Errorf("--verify of an edited store: exit %d\n%s", code, out)
	}

	// A login whose shell is not POSIX (csh's descendant tcsh, FreeBSD
	// root's before 14, and fish) gathers the same packages: every probe
	// runs in sh, so the login shell only passes it on. Read from the task's
	// own report, since the store would still hold the earlier gather's.
	for _, shell := range []string{"tcsh", "fish"} {
		asRoot(t, g.container, "apk add -q "+shell+` && sed -i -E "s#^(`+releaseGateSSHUser+`:([^:]*:){5})[^:]*\$#\\1$(command -v `+shell+`)#" /etc/passwd`)
		if got := asRoot(t, g.container, "getent passwd "+releaseGateSSHUser+" | cut -d: -f7"); !strings.HasSuffix(got, "/"+shell) {
			t.Fatalf("the login's shell is %q, not %s", got, shell)
		}
		out, code := g.pleiades(t, "adhoc", "container1", "facts.gather", "gather_subset:=[packages]", "--json")
		var rep struct {
			Tasks []struct {
				Stats map[string]any `json:"stats"`
			} `json:"tasks"`
		}
		if code != 0 || json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &rep) != nil || len(rep.Tasks) != 1 {
			t.Fatalf("facts.gather with a %s login: exit %d\n%s", shell, code, out)
		}
		n := 0
		for key := range rep.Tasks[0].Stats {
			if strings.HasPrefix(key, "package.") {
				n++
			}
		}
		// Counted again: installing the shell added packages.
		if now := asRoot(t, g.container, "apk list --installed 2>/dev/null | grep -c installed"); strconv.Itoa(n) != now {
			t.Errorf("with a %s login, %d package facts; apk lists %s", shell, n, now)
		}
	}
}

// numberOf is a JSON number as a float64, or -1.
func numberOf(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return -1
}

// asRoot runs script as root in c and returns its trimmed output.
func asRoot(t *testing.T, c testcontainers.Container, script string) string {
	t.Helper()
	code, r, err := c.Exec(context.Background(), []string{"sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("%s exited %d: %s", script, code, out)
	}
	return strings.TrimSpace(string(out))
}
