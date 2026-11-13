// Release gate for Phase 127b's "snapshots of more than FRR": through the
// real binary, a real IOS XE router's own running configuration (the CML
// sandbox's iol-xe node, read with net.ios.facts over Telnet, its only login
// service) is held to a real Batfish
// container's model as cisco_ios. Batfish parses it and models its routes;
// every line the fail-closed coverage refuses is named by the refusal and
// passes once accepted by name; and the snapshot Batfish receives, recorded
// on its way there, holds no secret.
package main_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// placeholderRE matches one placeholder netcfg.Mask writes.
var placeholderRE = regexp.MustCompile(regexp.QuoteMeta(netcfg.MaskPrefix) + `[0-9a-f]{16}`)

// TestCLI_BatfishModelsARealIOSXERunningConfig is the release gate.
func TestCLI_BatfishModelsARealIOSXERunningConfig(t *testing.T) {
	env := cmlGateEnv(t)
	if testing.Short() {
		t.Skip("starts Batfish")
	}
	node := env["PLEIADES_CML_NODE"]
	dir := t.TempDir()
	run := cmlRunner(t, env, dir)
	cmlImportNode(t, env, run, "telnet")
	cmlAddEnableCredential(t, env, run)
	proxy, uploads := recordSnapshotUploads(t, strings.Replace(testsupport.StartBatfish(t), "localhost", "127.0.0.1", 1))
	if out, err := run("", "add-host", "batfish", "--type", "batfish", "--set", "api_base="+proxy); err != nil {
		t.Fatalf("add-host batfish: %v\n%s", err, out)
	}

	// The configuration as the router holds it, read first so the model can
	// be asked about routes the router itself holds rather than ones this
	// test assumes.
	out, err := run("", "adhoc", node, "net.ios.facts", "gather_subset:=[\"config\"]", "insecure_telnet=true", "--json")
	facts := firstTask(t, out, err)
	stats, _ := facts["stats"].(map[string]any)
	config, _ := stats["ansible_net_config"].(string)
	if facts["status"] != "ok" || !strings.Contains(config, "\nhostname "+node) {
		t.Fatalf("net.ios.facts config = %v", facts)
	}
	assertions := iosRouteAssertions(node, config)
	if len(assertions) == 0 {
		t.Fatalf("the router's configuration holds no interface address or static route to ask the model about:\n%s", config)
	}

	// Accepting nothing: every line the model does not recognise or does not
	// model is refused, each named in the failure.
	path := filepath.Join("runbooks", "iosxe_batfish.yaml")
	writeCMLRunbook(t, dir, path, iosBatfishRunbook(node, assertions, nil))
	if out, err := run("", "validate", path); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	report, out, err := runReport(t, run, path)
	tasks := reportTasks(report)
	if len(tasks) != 2 || tasks[0]["status"] != "ok" {
		t.Fatalf("the run did not read the router: %v\n%s", err, out)
	}
	check, _ := tasks[1]["stats"].(map[string]any)
	if parse := fmt.Sprint(check["parse"]); !strings.Contains(parse, "PASSED") && !strings.Contains(parse, "PARTIALLY_UNRECOGNIZED") {
		t.Fatalf("Batfish did not parse the router's configuration: %v\n%s", check["parse"], out)
	}
	held(t, check, len(assertions))
	// What this run sent Batfish is held to the configuration this run read.
	snapshotHoldsNoSecret(t, env, uploads.take(t), ranConfig(tasks))
	var tripped []string
	unrecognized, _ := check["unrecognized"].([]any)
	for _, u := range unrecognized {
		row, _ := u.(map[string]any)
		text := fmt.Sprint(row["text"])
		tripped = append(tripped, text)
		if !strings.Contains(fmt.Sprint(tasks[1]["error"]), fmt.Sprintf("(%q) was not recognised by the model, and accept_unrecognized does not accept it", text)) {
			t.Errorf("the refusal does not name %q: %v", text, tasks[1]["error"])
		}
	}
	t.Logf("the router's configuration trips %d line(s) the model refuses: %q", len(tripped), tripped)
	if len(tripped) == 0 {
		if err != nil || tasks[1]["status"] != "ok" || check["passed"] != true {
			t.Fatalf("a configuration tripping nothing did not pass: %v\n%s", err, out)
		}
	} else {
		if err == nil || tasks[1]["status"] != "failed" || check["passed"] != false {
			t.Fatalf("unaccepted lines did not fail the check: %v\n%s", err, out)
		}
		// Each accepted by name with a reason: the check passes and reports
		// each as not modelled.
		writeCMLRunbook(t, dir, path, iosBatfishRunbook(node, assertions, tripped))
		report, out, err = runReport(t, run, path)
		tasks = reportTasks(report)
		if err != nil || len(tasks) != 2 || tasks[1]["status"] != "ok" {
			t.Fatalf("the check with every tripped line accepted: %v\n%s", err, out)
		}
		check, _ = tasks[1]["stats"].(map[string]any)
		held(t, check, len(assertions))
		snapshotHoldsNoSecret(t, env, uploads.take(t), ranConfig(tasks))
		for _, text := range tripped {
			if !strings.Contains(fmt.Sprint(check["not_modelled"]), text) {
				t.Errorf("not_modelled does not list the accepted %q: %v", text, check["not_modelled"])
			}
		}
	}
}

// ranConfig returns the configuration a run's first task, net.ios.facts,
// read.
func ranConfig(tasks []map[string]any) string {
	stats, _ := tasks[0]["stats"].(map[string]any)
	config, _ := stats["ansible_net_config"].(string)
	return config
}

// iosBatfishRunbook reads node's running configuration and holds it to the
// model as cisco_ios with assertions, accepting each line of accept.
func iosBatfishRunbook(node string, assertions, accept []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# What this does: reads %[1]s's running configuration and holds it to Batfish's model.
# Blast radius: none; %[1]s is only read.
# Changes: nothing on any device; Batfish keeps the snapshot until the task ends.
# Undo: nothing to undo.
id: iosxe_batfish
hosts: %[1]s
tasks:
  - name: Read %[1]s
    net.ios.facts:
      insecure_telnet: true
      gather_subset:
        - config
    register: device
  - name: The model parses and routes %[1]s's own configuration
    net.batfish.check:
      target: batfish
      devices:
        - {name: %[1]s, format: cisco_ios, config: "{{ result.device.ansible_net_config }}"}
      assertions:
`, node)
	for _, a := range assertions {
		b.WriteString("        - " + a + "\n")
	}
	if len(accept) > 0 {
		b.WriteString("      accept_unrecognized:\n")
		for _, text := range accept {
			fmt.Fprintf(&b, "        - {text: %q, reason: \"a management or platform setting, not routing\"}\n", text)
		}
	}
	return b.String()
}

// managementVRF is IOS XE's management VRF. Batfish deactivates every
// interface in it (measured on the sandbox's IOL XE router, 2026-11-13:
// Admin_Up true, Active false, and no route in the VRF), so the model holds
// none of its routes, and the check does not refuse it.
const managementVRF = "Mgmt-intf"

// iosRouteAssertions returns, as the check's assertions, a connected route
// for each address on an interface that is not shut down and a static route
// for each ip route the configuration holds, each in its own VRF. In the
// management VRF, which the model leaves out, it asserts instead that the
// model holds the VRF's interfaces up but inactive, which is what a passing
// check does not cover.
func iosRouteAssertions(node, config string) []string {
	var out []string
	iface, vrf, addr, shut := "", "default", "", false
	inactive := false
	flush := func() {
		switch {
		case iface == "" || addr == "" || shut:
		case vrf == managementVRF:
			inactive = true
		default:
			out = append(out, fmt.Sprintf("route: {node: %s, prefix: %s, protocol: connected, vrf: %s}", node, addr, vrf))
		}
		iface, vrf, addr, shut = "", "default", "", false
	}
	for _, raw := range strings.Split(config, "\n") {
		line := strings.TrimRight(raw, "\r")
		w := strings.Fields(line)
		switch {
		case !strings.HasPrefix(line, " "):
			flush()
			if len(w) == 2 && w[0] == "interface" {
				iface = w[1]
			}
			if len(w) >= 5 && w[0] == "ip" && w[1] == "route" {
				routeVRF := "default"
				if w[2] == "vrf" && len(w) >= 7 {
					routeVRF, w = w[3], append([]string{"ip", "route"}, w[4:]...)
				}
				if p, ok := prefixOf(w[2], w[3]); ok && routeVRF != managementVRF {
					if hop, err := netip.ParseAddr(w[4]); err == nil {
						out = append(out, fmt.Sprintf("route: {node: %s, prefix: %s, protocol: static, next_hops: [%s], vrf: %s}", node, p, hop, routeVRF))
					}
				}
			}
		case len(w) == 3 && w[0] == "vrf" && w[1] == "forwarding":
			vrf = w[2]
		case len(w) == 4 && w[0] == "ip" && w[1] == "address":
			if p, ok := prefixOf(w[2], w[3]); ok {
				addr = p
			}
		case len(w) == 1 && w[0] == "shutdown":
			shut = true
		}
	}
	flush()
	if inactive {
		out = append(out, fmt.Sprintf("question: {name: interfaceProperties, where: {VRF: %s, Admin_Up: true, Active: false}}", managementVRF))
	}
	return out
}

// prefixOf returns the network an IPv4 address and dotted mask name.
func prefixOf(addr, mask string) (string, bool) {
	a, err := netip.ParseAddr(addr)
	m := net.ParseIP(mask).To4()
	if err != nil || !a.Is4() || m == nil {
		return "", false
	}
	ones, bits := net.IPMask(m).Size()
	if bits == 0 {
		return "", false // not a contiguous mask
	}
	p, err := a.Prefix(ones)
	return p.String(), err == nil
}

// held fails the test unless the check reports want assertions, all held.
func held(t *testing.T, check map[string]any, want int) {
	t.Helper()
	rows, _ := check["assertions"].([]any)
	if len(rows) != want {
		t.Errorf("the check reports %d assertions, want %d: %v", len(rows), want, rows)
	}
	for _, r := range rows {
		if row, _ := r.(map[string]any); row["passed"] != true {
			t.Errorf("the model does not hold what the router holds: %v", row)
		}
	}
}

// snapshotHoldsNoSecret holds every configuration Batfish received to
// carrying no secret: each line the router's own masked configuration masks
// is masked in the upload, every placeholder follows its secret word (and at
// most an encryption type) directly, so no word of a secret stands before
// it, and the enable secret and the login's password appear nowhere.
func snapshotHoldsNoSecret(t *testing.T, env map[string]string, uploaded []string, ranConfig string) {
	t.Helper()
	if len(uploaded) == 0 {
		t.Fatal("no snapshot reached Batfish")
	}
	want := placeholderLines(ranConfig)
	for _, cfg := range uploaded {
		if !strings.Contains(cfg, "\nhostname "+env["PLEIADES_CML_NODE"]) {
			t.Errorf("the snapshot is not the router's configuration:\n%s", cfg)
		}
		if got := placeholderLines(cfg); !slices.Equal(got, want) {
			t.Errorf("the snapshot masks lines %v, the router's own configuration lines %v; the two begin:\n%q\n%q", got, want, head(cfg, 6), head(ranConfig, 6))
		}
		for i, line := range strings.Split(cfg, "\n") {
			w := strings.Fields(line)
			secretLine := len(w) > 1 && (w[0] == "enable" || w[0] == "username") && (slices.Contains(w, "secret") || slices.Contains(w, "password"))
			if secretLine && !placeholderRE.MatchString(line) {
				t.Errorf("snapshot line %d holds a login secret unmasked", i+1)
			}
			if k := slices.IndexFunc(w, placeholderRE.MatchString); k >= 0 && !secretWordBefore(w[:k]) {
				t.Errorf("snapshot line %d keeps a word of its secret before the placeholder: %s", i+1, placeholderRE.ReplaceAllString(line, "<masked>"))
			}
		}
		// Each secret is at least as long as the run's own masking set
		// takes; a shorter one (the sandbox's documented cisco) is a common
		// word, which the line checks above cover.
		for _, k := range []string{"PLEIADES_CML_NODE_PASSWORD", "PLEIADES_CML_NODE_ENABLE"} {
			if len(env[k]) >= netcfg.MinLearnedLength && strings.Contains(cfg, env[k]) {
				t.Errorf("the snapshot holds %s's value", k)
			}
		}
	}
}

// head returns config's first n lines, each placeholder replaced.
func head(config string, n int) []string {
	lines := strings.Split(placeholderRE.ReplaceAllString(config, "<masked>"), "\n")
	return lines[:min(n, len(lines))]
}

// placeholderLines returns the numbers of config's lines holding a
// placeholder.
func placeholderLines(config string) []int {
	var out []int
	for i, line := range strings.Split(config, "\n") {
		if placeholderRE.MatchString(line) {
			out = append(out, i+1)
		}
	}
	return out
}

// secretWordBefore reports whether the words before a placeholder end in a
// secret word, optionally followed by one encryption type, and optionally
// preceded by a privilege level: password, password 0, password level 15 0.
func secretWordBefore(w []string) bool {
	n := len(w)
	if n > 0 && len(w[n-1]) == 1 && w[n-1] >= "0" && w[n-1] <= "9" {
		n--
	}
	if n >= 2 && w[n-2] == "level" {
		n -= 2
	}
	return n > 0 && slices.Contains([]string{"password", "secret", "key", "key-string", "authentication-key", "community", "pre-shared-key"}, w[n-1])
}

// snapshotRecorder keeps every snapshot archive sent through it.
type snapshotRecorder struct {
	mu   sync.Mutex
	zips [][]byte
}

// recordSnapshotUploads returns the address of a proxy to the Batfish at
// target that keeps a copy of each snapshot uploaded through it, which is
// what the check sends and the only place its masking can be read from
// outside.
func recordSnapshotUploads(t *testing.T, target string) (string, *snapshotRecorder) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rec := &snapshotRecorder{}
	forward := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A snapshot is a zip archive POSTed to /v2/networks/<n>/snapshots/<s>;
		// a fork is a POST to .../snapshots:fork and carries no configuration.
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/snapshots/") && r.Header.Get("Content-Type") == "application/octet-stream" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			rec.mu.Lock()
			rec.zips = append(rec.zips, body)
			rec.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

// take returns the text of every configuration in every snapshot kept
// since the last take, and forgets them.
func (r *snapshotRecorder) take(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	zips := r.zips
	r.zips = nil
	var out []string
	for _, z := range zips {
		zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
		if err != nil {
			t.Fatalf("a snapshot is not a zip archive: %v", err)
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".cfg") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			text, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(text))
		}
	}
	return out
}

// firstTask returns the one task of an adhoc --json report.
func firstTask(t *testing.T, out string, err error) map[string]any {
	t.Helper()
	var report struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&report) != nil || len(report.Tasks) != 1 {
		t.Fatalf("adhoc: %v\n%s", err, out)
	}
	return report.Tasks[0]
}

// runReport runs a runbook with --json and returns its report, failing the
// test only when it printed none.
func runReport(t *testing.T, run func(string, ...string) (string, error), path string) (map[string]any, string, error) {
	t.Helper()
	out, err := run("", "run", path, "--json")
	return decodeReport(t, out, err), out, err
}

// decodeReport returns the run report out begins with, failing the test
// when it holds none. Standard error follows the report in combined output,
// so only the first JSON value is read.
func decodeReport(t *testing.T, out string, err error) map[string]any {
	t.Helper()
	var report map[string]any
	if json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&report) != nil {
		t.Fatalf("the run printed no report: %v\n%s", err, out)
	}
	return report
}

// reportTasks returns a run report's tasks.
func reportTasks(report map[string]any) []map[string]any {
	var out []map[string]any
	tasks, _ := report["tasks"].([]any)
	for _, tk := range tasks {
		task, _ := tk.(map[string]any)
		out = append(out, task)
	}
	return out
}
