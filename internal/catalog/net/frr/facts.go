// Package frr: net.frr.facts, a router's state as data, the secrets its
// configuration holds masked (pkg/netcfg) before anything reads it.
package frr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// factsFQCN is net.frr.facts's name.
const factsFQCN = "net.frr.facts"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: factsFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility:        collection.Reversibility{ReadOnly: true, Notes: "Runs only show commands, which change nothing."},
			SupportsCheck:        true,
			Doc: collection.Doc{
				Summary:     "Reads an FRRouting router's version, interfaces, OSPF neighbours, routes and BGP peers as data.",
				Description: "Asks vtysh for each in JSON and returns it as data, and by default also the running configuration with every secret in it (a password, an authentication or message-digest key, a neighbour's password) replaced by a placeholder that is the same for equal secrets throughout one command-line run, so a model can still compare two routers' keys without seeing one (a Runner reads each device apart, so there placeholders compare only within one router). The word after each secret word (password, secret, authentication-key, message-digest-key, key-string, community, key) is masked, past any encryption type, key id or algorithm between them. It changes nothing.",
				Params: []collection.Param{
					{Name: "include_config", Type: "bool", Default: "true", Description: "Return the running configuration, its secrets replaced, the default; false leaves it out (config_sha256 is returned either way)."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "version", Type: "string", Returned: "always", Description: "The FRR version."},
					{Name: "hostname", Type: "string", Returned: "always", Description: "The router's hostname as FRR knows it."},
					{Name: "interfaces", Type: "dict", Returned: "always", Description: "Each interface, from show interface json."},
					{Name: "ospf_neighbors", Type: "dict", Returned: "always", Description: "show ip ospf neighbor json, empty when OSPF is not running."},
					{Name: "routes", Type: "dict", Returned: "always", Description: "show ip route json: every route by prefix."},
					{Name: "bgp_summary", Type: "dict", Returned: "always", Description: "show bgp summary json, empty when BGP is not running."},
					{Name: "config_sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the running configuration as it runs (from its frr version line), in hex: equal to a candidate file's digest exactly when the router runs that file."},
					{Name: "running_config", Type: "string", Returned: "unless include_config is false", Description: "The running configuration with its secrets replaced."},
				},
				Examples: []collection.Example{
					{Name: "Read a router for a model", RunbookYAML: "- name: Read r1\n  net.frr.facts:\n  register: r1\n"},
				},
				SeeAlso: []string{"net.frr.command", "net.batfish.check"},
			},
		},
		Invoke: Facts,
		Check:  Facts,
	})
}

// Facts reads a router's version, interfaces, OSPF neighbours, routes and
// BGP peers, and unless include_config is false its running configuration,
// secrets replaced.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	s, err := open(ctx, rc, device, params, factsFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()

	out, err := s.vtysh(ctx, "show version")
	if err != nil {
		return collection.Result{}, err
	}
	version, hostname, err := parseVersion(out)
	if err != nil {
		return collection.Result{}, err
	}
	daemons, err := s.daemons(ctx)
	if err != nil {
		return collection.Result{}, err
	}
	stats := map[string]any{"version": version, "hostname": hostname}
	// A daemon that is not running has nothing to say, and vtysh says so
	// with a "% " line rather than an empty answer, so each is asked only
	// when it runs.
	reads := []struct {
		stat, daemon, command string
	}{
		{"interfaces", "zebra", "show interface json"},
		{"routes", "zebra", "show ip route json"},
		{"ospf_neighbors", "ospfd", "show ip ospf neighbor json"},
		{"bgp_summary", "bgpd", "show bgp summary json"},
	}
	for _, r := range reads {
		data := map[string]any{}
		if slices.Contains(daemons, r.daemon) {
			if data, err = s.vtyshJSON(ctx, r.command); err != nil {
				return collection.Result{}, err
			}
		}
		stats[r.stat] = data
	}
	config, err := s.runningConfig(ctx)
	if err != nil {
		return collection.Result{}, err
	}
	// The digest of the configuration as it runs, secrets and all, so it
	// compares with a candidate file's (net.frr.config's sha256) while the
	// text a reader sees stays masked.
	sum := sha256.Sum256([]byte(config))
	stats["config_sha256"] = hex.EncodeToString(sum[:])
	// The masked configuration is returned unless the task asks it not to
	// be (Phase 131's R5): it was read for the digest anyway.
	include, err := sdk.BoolParamOr(params, "include_config", true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", factsFQCN, err)
	}
	if include {
		stats["running_config"] = netcfg.Mask(config)
	}
	for _, k := range []string{"version", "hostname", "interfaces", "routes", "ospf_neighbors", "bgp_summary", "config_sha256", "running_config"} {
		if v, ok := stats[k]; ok {
			if err := rc.SetStat(k, v); err != nil {
				return collection.Result{}, err
			}
		}
	}
	return collection.Result{}, nil
}

// parseVersion reads the version and hostname from show version's first
// line, "FRRouting 10.1.1 (r1) on Linux(...).".
func parseVersion(out string) (version, hostname string, err error) {
	fields := strings.Fields(versionLine(out))
	if len(fields) < 3 || fields[0] != "FRRouting" || !strings.HasPrefix(fields[2], "(") || !strings.HasSuffix(fields[2], ")") {
		return "", "", fmt.Errorf("%s: show version did not answer as FRRouting does: %s", factsFQCN, farside.MaskedOutput(out, netcfg.Mask))
	}
	hostname = fields[2][1 : len(fields[2])-1]
	if !hostnameWord.MatchString(hostname) {
		return "", "", fmt.Errorf("%s: show version names the hostname %q, which is not one", factsFQCN, hostname)
	}
	return fields[1], hostname, nil
}

// versionLine is show version's first non-empty line, the one that names
// the release and the hostname. It is parsed, never quoted in an error:
// an answer it cannot parse is quoted whole through pkg/farside.
func versionLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// hostnameWord is what a hostname show version prints may be.
var hostnameWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

// daemons returns the FRR daemons running, from show daemons.
func (s *session) daemons(ctx context.Context) ([]string, error) {
	out, err := s.vtysh(ctx, "show daemons")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}
