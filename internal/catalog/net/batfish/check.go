// Package batfish implements net.batfish.check: configurations held to
// Batfish's model of the network before any of them is applied.
package batfish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	bf "github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// checkFQCN is net.batfish.check's name.
const checkFQCN = "net.batfish.check"

// baseSnapshot is the snapshot of the configurations as given.
const baseSnapshot = "given"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: checkFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportHTTPS},
			RequiredCapabilities: []capability.Name{capability.NameBatfishAPI},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It asks Batfish about configurations and changes no device; the snapshot it uploads is removed when the task ends.",
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Checks configurations against Batfish's model of the network before any of them is applied, and fails closed.",
				Description: "Builds a snapshot from each device's whole configuration (config, or src: a file in the project), its secrets masked before anything is sent (equal secrets alike, so key mismatches still show), asks Batfish to parse it, and holds the model to the assertions given: OSPF sessions established, a route present (through the next hops named) or absent, a flow delivered or not and through or around the nodes named, and a node's modelled routes equal to the routes net.frr.facts read from it live. Each scenario forks the snapshot with interfaces or nodes down and holds it to its own assertions, so a link failure is checked before it happens. Its target is the batfish device. FRR configurations go to Batfish in the layout it reads FRR in, with each ip ospf passive moved under router ospf; Cisco IOS ones go as they are. It fails closed: an assertion that does not hold, a file Batfish could not parse, a question it could not answer, or a line it did not recognise that the task does not accept by name with a reason (lines with no meaning of their own, exit, end and !, need none). The result reports three things apart: whether the model parsed (each file's status and every unrecognised line), whether it agrees (each assertion), and what it does not model (the lines accepted). It returns the SHA-256 of each configuration as given, for net.frr.config's expect_sha256, so the change applied is the one checked. It changes nothing on any device; Batfish keeps the snapshot in a network of the run's own, removed when the task ends. A model that agrees is evidence, not proof: rehearse on the same OS image, and verify the change live.",
				Params: []collection.Param{
					{Name: "devices", Type: "list of dict", Required: true, Description: "Each device's whole configuration: name (the node's name, which a Cisco configuration's hostname must match), format (frr, the default, or cisco_ios), and config (the text, e.g. a registered net.frr.facts result's running_config) or src (a file in the project, read by the command line only)."},
					{Name: "assertions", Type: "list of dict", Description: "What must hold of the configurations as given, each one of: ospf_established: true (every OSPF session Batfish finds is established, and there is one); bgp_established: true (the same for BGP); route: {node, prefix, next_hops, protocol}; no_route: {node, prefix}; reach: {from (node or node[interface]), dst (an address), delivered (default true), through (nodes), avoids (nodes)}; routes_match: {node, routes (net.frr.facts' routes for that node), ignore (prefixes the configurations do not hold, such as a management network)}, comparing connected, static, OSPF, BGP, IS-IS, EIGRP and RIP routes by prefix, protocol and next hop."},
					{Name: "scenarios", Type: "list of dict", Description: "Each a name, down_interfaces (node[interface], e.g. r1[eth2]) and down_nodes, and its own assertions, checked on the snapshot with those down."},
					{Name: "accept_unrecognized", Type: "list of dict", Description: "Lines Batfish did not recognise that the task accepts, each text (the line, trimmed; a line holding a secret is matched without it, so ip ospf authentication-key accepts that line) and reason (why the model may ignore it). Every other unrecognised line fails the task."},
				},
				Returns: []collection.ReturnField{
					{Name: "passed", Type: "bool", Returned: "always", Description: "Whether every assertion held and nothing went unrecognised unaccepted."},
					{Name: "sha256", Type: "dict", Returned: "always", Description: "Each device's configuration's SHA-256, in hex, over the bytes as given."},
					{Name: "parse", Type: "list of dict", Returned: "always", Description: "Each file's parse status (device, status)."},
					{Name: "unrecognized", Type: "list of dict", Returned: "always", Description: "Each unrecognised line (device, line, text) and, when accepted, why."},
					{Name: "assertions", Type: "list of dict", Returned: "always", Description: "Each assertion (scenario, kind, passed, detail)."},
					{Name: "not_modelled", Type: "list of string", Returned: "always", Description: "What the model ignores: each accepted line, with its reason."},
				},
				Examples: []collection.Example{
					{Name: "Check a candidate and a link failure first", RunbookYAML: "- name: Read r1 as it runs\n  net.frr.facts:\n    target: r1\n    include_config: true\n  register: r1\n- name: Read r2 as it runs\n  net.frr.facts:\n    target: r2\n    include_config: true\n  register: r2\n- name: The model routes both LANs with r3's change, and without r1's link to r2\n  net.batfish.check:\n    target: batfish\n    devices:\n      - {name: r1, config: \"{{ result.r1.running_config }}\"}\n      - {name: r2, config: \"{{ result.r2.running_config }}\"}\n      - {name: r3, src: configs/r3.frr.conf}\n    assertions:\n      - ospf_established: true\n      - route: {node: r1, prefix: 10.3.3.0/24}\n    scenarios:\n      - name: r1-r2 down\n        down_interfaces: [\"r1[eth1]\"]\n        assertions:\n          - reach: {from: \"r1[eth3]\", dst: 10.3.3.10, through: [r3]}\n    accept_unrecognized:\n      - {text: domainname localdomain, reason: DNS search domain, not routing}\n  register: gate\n"},
				},
				SeeAlso: []string{"net.frr.config", "net.frr.facts", "wait.routes", "net.trace"},
			},
		},
		Invoke: Check,
		Check:  Check,
	})
}

// Check holds the configurations given to Batfish's model.
func Check(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	svc, ok := device.(capability.BatfishAPICapable)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: %s is not a Batfish service", checkFQCN, device.Name())
	}
	req, err := readParams(rc, params)
	if err != nil {
		return collection.Result{}, err
	}
	var warnings []string
	if w := svc.BatfishPlaintextWarning(); w != "" {
		warnings = append(warnings, w)
	}
	settings := devicetls.For(device)
	warnings = append(warnings, settings.Warnings(device.Name())...)
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, err
	}
	client, err := newClient(rc, svc.BatfishBaseURL(), settings)
	if err != nil {
		return collection.Result{}, err
	}

	digests := map[string]any{}
	files := map[string]string{}
	for _, d := range req.devices {
		sum := sha256.Sum256([]byte(d.config))
		digests[d.name] = hex.EncodeToString(sum[:])
		masked := netcfg.Mask(d.config)
		if d.format == formatFRR {
			if masked, err = netcfg.FRRForBatfish(d.name, masked); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", checkFQCN, err)
			}
		}
		files[d.name] = masked
	}
	if err := rc.SetStat("sha256", digests); err != nil {
		return collection.Result{}, err
	}
	zip, err := bf.Snapshot(files)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", checkFQCN, err)
	}
	network, err := bf.NewNetworkName()
	if err != nil {
		return collection.Result{}, err
	}
	if err := client.CreateNetwork(ctx, network); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", checkFQCN, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = client.DeleteNetwork(cleanup, network)
	}()
	if err := client.UploadSnapshot(ctx, network, baseSnapshot, zip); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", checkFQCN, err)
	}

	m := &model{client: client, network: network}
	var problems []string
	parse, unrecognized, notModelled, refused, err := m.consistency(ctx, req)
	if err != nil {
		return collection.Result{}, err
	}
	problems = append(problems, refused...)
	results, err := m.holdAll(ctx, baseSnapshot, "", req.assertions)
	if err != nil {
		return collection.Result{}, err
	}
	for i, sc := range req.scenarios {
		snapshot := fmt.Sprintf("scenario-%d", i+1)
		if err := client.Fork(ctx, network, baseSnapshot, snapshot, sc.interfaces, sc.nodes); err != nil {
			return collection.Result{}, fmt.Errorf("%s: scenario %s: %w", checkFQCN, sc.name, err)
		}
		more, err := m.holdAll(ctx, snapshot, sc.name, sc.assertions)
		if err != nil {
			return collection.Result{}, err
		}
		results = append(results, more...)
	}
	var rows []any
	for _, r := range results {
		rows = append(rows, r.row())
		if !r.passed {
			problems = append(problems, r.String())
		}
	}
	if rows == nil {
		rows = []any{}
	}
	for k, v := range map[string]any{
		"parse": parse, "unrecognized": unrecognized, "not_modelled": notModelled,
		"assertions": rows, "passed": len(problems) == 0,
	} {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	if len(problems) > 0 {
		return collection.Result{}, fmt.Errorf("%s: the model does not pass: %s", checkFQCN, strings.Join(problems, "; "))
	}
	return collection.Result{}, nil
}

// newClient builds the client for base: the device's TLS for https://,
// and the device's route when the run has one.
func newClient(rc sdk.RunbookContext, base string, settings devicetls.Settings) (*bf.Client, error) {
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Minute}
	if strings.HasPrefix(base, "https://") {
		cfg, err := settings.Config(nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", checkFQCN, err)
		}
		transport.TLSClientConfig = cfg
	}
	if r, ok := rc.(sdk.Router); ok {
		if dial := r.DeviceRoute(); dial != nil {
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dial(ctx, network, addr)
			}
		}
	}
	return bf.New(base, &http.Client{Transport: transport})
}
