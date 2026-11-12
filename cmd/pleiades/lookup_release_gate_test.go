// Release Gate for absence as data (Phase 131, R4) through the real binary:
// a lookup that cannot look fails, in a run and in a check, and never
// answers exists false. "Could not look" read as "not there" is the edge
// that makes absence as data dangerous: an agent told a file or a lab is
// not there acts on that, and a method that believed it would create over
// what it could not see.
package main_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// lookupModes are the two ways a lookup runs: a run, and a check, whose
// answer must be the same read.
var lookupModes = [][]string{nil, {"--mode", "check"}}

// TestALookupThatCannotLookFails: file.stat of a path under a directory the
// login may not search, file.stat of a host nothing answers at,
// virt.cml.lab.info against a controller that refuses the read, that
// nothing answers at, or whose name does not resolve, and a device whose
// console server names nothing or answers at no port, each fail the task in
// a run and in a check and never report exists false. The controls are the
// same lookups of what is really not there, which answer exists false with
// exit 0, so the failures above are the inability and not the absence.
func TestALookupThatCannotLookFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	dir, home, _ := sshdGateProject(t, "", "")
	ctl := cmltest.New(t, "admin", "pw")
	pin := devicetls.Fingerprint(ctl.Certificate())
	secret := cmlLab(t, ctl, "secret")
	dead := strconv.Itoa(closedPort(t))
	for _, args := range [][]string{
		{"add-host", "gone", "--type", "linux_server", "--set", "host=127.0.0.1", "--set", "port=" + dead},
		{"add-credential", "gone", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"add-host", "cml", "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + pin},
		{"add-credential", "cml", "--username", "admin", "--password", "pw"},
		{"add-host", "cml-down", "--type", "cml_controller", "--set", "api_base=https://127.0.0.1:" + dead, "--set", "tls_fingerprint=" + pin, "--set", "console_port=" + dead},
		{"add-credential", "cml-down", "--username", "admin", "--password", "pw"},
		// .invalid never resolves (RFC 6761), so this controller's name
		// cannot be turned into an address.
		{"add-host", "cml-nowhere", "--type", "cml_controller", "--set", "api_base=https://cml.pleiades.invalid", "--set", "tls_fingerprint=" + pin},
		{"add-credential", "cml-nowhere", "--username", "admin", "--password", "pw"},
		{"add-host", "r1", "--type", "cisco_router", "--set", "transport=console", "--set", "console_server=cml-nothing", "--set", "console_line=00000000-0000-4000-8000-000000000001"},
		{"add-credential", "r1", "--username", "cisco", "--password", "cisco"},
		{"add-host", "r2", "--type", "cisco_router", "--set", "transport=console", "--set", "console_server=cml-down", "--set", "console_line=00000000-0000-4000-8000-000000000002"},
		{"add-credential", "r2", "--username", "cisco", "--password", "cisco"},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	lookup := func(t *testing.T, mode []string, host string, call ...string) (status string, stats map[string]any, errText, raw string, code int) {
		t.Helper()
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, append(append([]string{"adhoc", host}, call...), mode...)...)
		if len(rep.Tasks) != 1 {
			t.Fatalf("%s %v %v: want one task in the report:\n%s", host, call, mode, raw)
		}
		return rep.Tasks[0].Status, rep.Tasks[0].Stats, rep.Tasks[0].Error, raw, code
	}

	// The controls: what is really not there is exists false, exit 0.
	for _, mode := range lookupModes {
		for _, c := range []struct {
			host string
			call []string
		}{
			{"box", []string{"file.stat", "path=/tmp/r4-never-there"}},
			{"box", []string{"file.stat", "path=/tmp/r4-no-dir/r4-never-there"}},
			{"cml", []string{"virt.cml.lab.info", "lab=never-made"}},
		} {
			status, stats, errText, raw, code := lookup(t, mode, c.host, c.call...)
			if code != 0 || status != "ok" || stats["exists"] != false {
				t.Errorf("control %v %v: exit %d, %s, exists %v (%s):\n%s", c.call, mode, code, status, stats["exists"], errText, raw)
			}
		}
	}

	for _, c := range []struct {
		name, host, want string
		call             []string
		// fail and refuse are what the controller answers 500 and the
		// mapped status to while the case runs (cmltest's Fail and Refuse).
		fail   map[string]bool
		refuse map[string]int
	}{
		// testuser may not search /root, so test -e answers false for
		// whatever is under it.
		{name: "a path under a directory the login may not search", host: "box", want: "cannot look",
			call: []string{"file.stat", "path=/root/.profile"}},
		{name: "a path deeper under it", host: "box", want: "cannot look",
			call: []string{"file.stat", "path=/root/no/such/file"}},
		{name: "a host nothing answers at", host: "gone",
			call: []string{"file.stat", "path=/tmp/r4-never-there"}},
		{name: "a controller that fails the list", host: "cml", want: "500",
			call: []string{"virt.cml.lab.info", "lab=never-made"}, fail: map[string]bool{"GET /labs": true}},
		{name: "a lab the login may not read, named by title", host: "cml", want: "403",
			call: []string{"virt.cml.lab.info", "lab=secret"}, refuse: map[string]int{"GET /labs/" + secret: 403}},
		{name: "a lab the login may not read, named by id", host: "cml", want: "403",
			call: []string{"virt.cml.lab.info", "lab=" + secret}, refuse: map[string]int{"GET /labs/" + secret: 403}},
		{name: "a controller nothing answers at", host: "cml-down",
			call: []string{"virt.cml.lab.info", "lab=never-made"}},
		{name: "a controller whose name does not resolve", host: "cml-nowhere",
			call: []string{"virt.cml.lab.info", "lab=never-made"}},
		{name: "a console server that names no device", host: "r1", want: "console server",
			call: []string{"net.ios.facts"}},
		{name: "a console server nothing answers at", host: "r2", want: "console server",
			call: []string{"net.ios.facts"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl.Mu.Lock()
			ctl.Fail, ctl.Refuse = c.fail, c.refuse
			ctl.Mu.Unlock()
			t.Cleanup(func() { ctl.Mu.Lock(); ctl.Fail, ctl.Refuse = nil, nil; ctl.Mu.Unlock() })
			for _, mode := range lookupModes {
				status, stats, errText, raw, code := lookup(t, mode, c.host, c.call...)
				if code == 0 || status != "failed" {
					t.Errorf("%v: exit %d, %s, want the task failed:\n%s", mode, code, status, raw)
				}
				if exists, ok := stats["exists"]; ok {
					t.Errorf("%v: a lookup that could not look reported exists %v:\n%s", mode, exists, raw)
				}
				if !strings.Contains(errText, c.want) {
					t.Errorf("%v: the error %q does not say %q", mode, errText, c.want)
				}
			}
		})
	}
}

// cmlLab makes a lab titled title on ctl and returns its id.
func cmlLab(t *testing.T, ctl *cmltest.Controller, title string) string {
	t.Helper()
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: ctl.Username, Password: ctl.Password})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	return lab.ID
}

// closedPort returns a local TCP port that was just listened on and closed,
// so a dial to it is refused.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
