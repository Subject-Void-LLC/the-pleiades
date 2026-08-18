package main_test

import (
	"os"
	"strings"
	"testing"
)

// This file is the WinRM Release Gate: the real built binary, driven
// through init, add-host, add-credential and run, against a real Windows
// host over real WinRM with NTLM and SPNEGO message encryption.
//
// # Why this one is env-gated instead of container-backed
//
// Every other gate in this package starts an ephemeral container and is
// therefore unconditional. There is no Windows container a Linux CI box
// can run, and no way to fake WinRM without also faking the thing under
// test, so this gate needs a real Windows host and skips clearly when it
// does not have one. That makes it weaker than the container gates by
// construction, and the honest response is to say so rather than to
// build a mock that would pass while proving nothing.
//
// # Why it changes the network configuration, of all things
//
// Because that is the operation that exposed the risk. Converting an
// adapter from DHCP to a static address tears down the very channel the
// instruction arrived on, which is the hardest shape of remote change
// there is: the command's own success cannot be reported over the
// connection it destroys. A gate that only ran "hostname" would prove
// WinRM connects and nothing about whether this platform can perform a
// change that costs it its own transport.
//
// # The reverse is not optional here
//
// This gate performs the conversion AND converts back, and asserts the
// adapter ends where it started. Two reasons, and the second is the one
// that matters. A gate that leaves a lab machine reconfigured can be run
// exactly once, so it is not a gate, it is a one-way migration with
// assertions. And the reverse is the reversibility contract's own claim
// made executable: a change this platform performs should be a change it
// can describe how to undo, and the cheapest proof of that is undoing it.
//
// A real incident is behind this. An earlier, ungated run of this same
// conversion against a lab VM left it unreachable, with the static
// address apparently never taking and no path back in. The revert half
// below is what that cost bought.
//
// # Never convert twice in one session, and the reason is measured
//
// This gate converts ONCE and reverts once. An earlier version had a
// second test that converted, reverted, and converted again to prove
// idempotence, and that second conversion took the lab machine down with
// ARP failing for its address, which is a machine with no usable IPv4
// rather than a filtered one.
//
// The mechanism is known rather than guessed, because the console
// showed it: the adapter came back with "DHCP Enabled: No" and its only
// IPv4 address was an APIPA 169.254 one. netsh disables DHCP and then
// binds the static address, and when that address is the one the
// interface still holds from its own lease, the bind fails after the
// disable has already taken. What is left is an adapter with DHCP off
// and nothing bound, which is a machine with no usable IPv4 rather than
// a filtered one. That also rules out the firewall: the WinRM Public
// rule was enabled for this run and it went dark anyway, and a firewall
// filters IP while still answering ARP.
//
// The first conversion in a session does not hit this. One immediately
// after a revert does, because the lease has just been reissued.
//
// Whatever the exact mechanism, the operational rule stands on its own:
// this operation is not safe to repeat back-to-back against the same
// address, so the gate does not. Proving idempotence needs a second
// address or a settling period, and neither is worth another outage
// until somebody actually needs that proof.
//
// # PLEIADES_WINRM_IP must not be the address the host already has
//
// This follows directly from the paragraph above and is the single most
// important thing to get right before running this. Point it at an
// address reserved for the host and OUTSIDE the DHCP pool.
// requireSafeToConvert checks it and skips rather than proceeding, but
// the check exists because the mistake is easy and expensive, not
// because it makes the mistake harmless.

// winrmGateEnv names every variable this gate needs. Listing them in one
// place lets the skip message tell an operator exactly what to set rather
// than making them read the test.
const (
	envWinRMHost     = "PLEIADES_WINRM_HOST"
	envWinRMUser     = "PLEIADES_WINRM_USER"
	envWinRMPassword = "PLEIADES_WINRM_PASSWORD"
	envWinRMAdapter  = "PLEIADES_WINRM_ADAPTER"
	envWinRMIP       = "PLEIADES_WINRM_IP"
	envWinRMMask     = "PLEIADES_WINRM_MASK"
	envWinRMGateway  = "PLEIADES_WINRM_GATEWAY"
	envWinRMDNS      = "PLEIADES_WINRM_DNS"
)

// winrmGateConfig is the resolved lab target.
type winrmGateConfig struct {
	host, user, password       string
	adapter, ip, mask, gateway string
	dns                        string
}

// winrmGate reads the environment, skipping the test when the lab target
// is not configured.
//
// It requires the network values to be supplied rather than discovering
// them, deliberately. Discovering the current address and writing it back
// as static is what the runbook does; a gate that derived its expected
// values from the same read it is checking would agree with itself no
// matter what happened.
func winrmGate(t *testing.T) winrmGateConfig {
	t.Helper()

	cfg := winrmGateConfig{
		host:     os.Getenv(envWinRMHost),
		user:     os.Getenv(envWinRMUser),
		password: os.Getenv(envWinRMPassword),
		adapter:  os.Getenv(envWinRMAdapter),
		ip:       os.Getenv(envWinRMIP),
		mask:     os.Getenv(envWinRMMask),
		gateway:  os.Getenv(envWinRMGateway),
		dns:      os.Getenv(envWinRMDNS),
	}

	var missing []string
	for name, value := range map[string]string{
		envWinRMHost: cfg.host, envWinRMUser: cfg.user, envWinRMPassword: cfg.password,
		envWinRMAdapter: cfg.adapter, envWinRMIP: cfg.ip, envWinRMMask: cfg.mask,
		envWinRMGateway: cfg.gateway, envWinRMDNS: cfg.dns,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("WinRM Release Gate needs a real Windows host; set %s. "+
			"This gate RECONFIGURES the target's network adapter and converts it back again, so point it only at a lab machine you can reach at the console.",
			strings.Join(missing, ", "))
	}
	return cfg
}

// winrmGateProject builds a project directory pointed at the lab host.
func winrmGateProject(t *testing.T, cfg winrmGateConfig) string {
	t.Helper()

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "win-gate",
		"--type", "windows_server", "--set", "host="+cfg.host); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-credential", "win-gate",
		"--username", cfg.user, "--password", cfg.password); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	return dir
}

// writeRunbook drops a runbook into the project and returns its path
// relative to the project directory, which is what run takes.
func writeRunbook(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := "runbooks/" + name + ".yaml"
	if err := os.WriteFile(dir+"/"+path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// adapterAddress is one IPv4 address currently bound to the target
// adapter, with where it came from.
type adapterAddress struct {
	ip     string
	origin string
}

// requireSafeToConvert reads the target adapter's actual state and
// refuses the conversion unless it is safe to attempt, turning each
// known way this operation destroys a machine into a skip that says what
// to change.
//
// Two distinct hazards, and getting their order of importance wrong is
// itself part of this file's history.
//
// The one that has actually happened, twice, is converting the adapter
// to the address it already holds from its own DHCP lease. netsh
// disables DHCP and then binds the static address, and when that address
// is the leased one the bind fails after the disable has already taken.
// What survives is an adapter with DHCP off and nothing bound, falling
// back to APIPA: a machine with no usable IPv4 and the change half
// applied. FAILURE_PATTERNS.md #159. Checking this is the whole reason
// the gate takes the target address from the environment rather than
// discovering it.
//
// The second is real but was never the cause. Changing an adapter's
// address makes Network Location Awareness treat the result as a NEW
// network, and on a host with no domain controller it classifies it
// Public; the rule Enable-PSRemoting creates, WINRM-HTTP-In-TCP, covers
// Domain and Private only, so WinRM would stop answering the moment the
// address changed. WINRM-HTTP-In-TCP-PUBLIC is what covers that case and
// it is off by default. It is checked here because it is a genuine way
// to lose the host, not because it explains the outages: the Public rule
// was enabled for the run that went dark, and it went dark anyway.
//
// Everything is read in one round trip, and every failure is a skip
// rather than a fix. Enabling a firewall rule or releasing a lease on
// someone's machine is a durable change to it, and a test that quietly
// made one would be doing something well outside what a test should do.
func requireSafeToConvert(t *testing.T, dir string, cfg winrmGateConfig) {
	t.Helper()

	rb := writeRunbook(t, dir, "precondition", `id: precondition
hosts: win-gate
tasks:
  - name: Report the adapter's current addresses and the Public WinRM rule
    exec.winrm.shell:
      shell: powershell
      command: |
        foreach ($a in @(Get-NetIPAddress -InterfaceAlias `+cfg.adapter+` -AddressFamily IPv4 -ErrorAction SilentlyContinue)) {
            "current={0}|{1}" -f $a.IPAddress, $a.PrefixOrigin
        }
        $public = Get-NetFirewallRule -Name 'WINRM-HTTP-In-TCP-PUBLIC' -ErrorAction SilentlyContinue
        if ($public -and $public.Enabled -eq 'True') { 'winrm-public-rule=enabled' } else { 'winrm-public-rule=disabled' }
`)

	out, err := runPleiades(t, dir, "run", rb, "--verbose")
	if err != nil {
		t.Fatalf("the precondition task could not read the adapter's state: %v\n%s", err, out)
	}

	current := parseAdapterAddresses(out)
	if len(current) == 0 {
		t.Skipf("refusing to convert the adapter: %s reports no IPv4 address at all on %q, so either the "+
			"adapter name is wrong or this host is already in the half-converted state this gate exists to "+
			"avoid creating.\n%s", cfg.host, cfg.adapter, out)
	}

	for _, addr := range current {
		switch {
		case addr.ip == cfg.ip && addr.origin == "Dhcp":
			t.Skipf("refusing to convert the adapter: %s is the address %s already holds from its own DHCP "+
				"lease, and converting an adapter to its leased address disables DHCP and then fails to bind, "+
				"leaving the host on APIPA with no IPv4 (FAILURE_PATTERNS.md #159). Set %s to an address "+
				"OUTSIDE the DHCP pool, reserved for this host.", cfg.ip, cfg.host, envWinRMIP)

		case addr.ip == cfg.ip && addr.origin == "Manual":
			t.Skipf("skipping: %s is already statically configured at %s, so there is no DHCP-to-static "+
				"transition left for this gate to exercise. Put the adapter back on DHCP first, or point %s "+
				"at a different address.", cfg.host, cfg.ip, envWinRMIP)

		case strings.HasPrefix(addr.ip, "169.254."):
			t.Skipf("refusing to convert the adapter: %s currently holds the APIPA address %s, which is what a "+
				"half-applied conversion leaves behind. Put this host back on a working DHCP lease at the "+
				"console before running the gate again.", cfg.host, addr.ip)
		}
	}

	if !strings.Contains(out, "winrm-public-rule=enabled") {
		t.Skipf("refusing to convert the adapter: WINRM-HTTP-In-TCP-PUBLIC is not enabled on the target, "+
			"so changing its address would re-identify the network as Public and cut WinRM off mid-change, "+
			"leaving the machine reachable only at the console. Enable that rule, or set the connection "+
			"profile to Private, before running this gate.\n%s", out)
	}
}

// TestParseAdapterAddresses covers the precondition's parser without a
// Windows host, which is the only part of this file that can be.
//
// It is worth having precisely because everything around it skips. The
// check this parser feeds is what stands between a run and the failure
// that has taken this lab machine down twice, and a parser that silently
// returned nothing would turn that check into an unconditional pass
// while every visible signal (the gate runs, the gate passes) looked
// exactly the same.
func TestParseAdapterAddresses(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []adapterAddress
	}{
		{
			name: "verbose run output, indented under the stat key",
			out: "  tasks[0] [abc]: changed\n" +
				"    stdout:\n" +
				"      current=10.0.0.246|Dhcp\n" +
				"      winrm-public-rule=enabled\n",
			want: []adapterAddress{{ip: "10.0.0.246", origin: "Dhcp"}},
		},
		{
			name: "more than one address on the same adapter",
			out: "      current=10.0.0.246|Dhcp\n" +
				"      current=169.254.45.78|WellKnown\n",
			want: []adapterAddress{
				{ip: "10.0.0.246", origin: "Dhcp"},
				{ip: "169.254.45.78", origin: "WellKnown"},
			},
		},
		{
			// An adapter with no IPv4 prints no current= line at all, and
			// the caller turns that into its own skip. It must not be
			// confused with a parse failure.
			name: "no addresses",
			out:  "  tasks[0] [abc]: changed\n    stdout:\n      winrm-public-rule=disabled\n",
			want: nil,
		},
		{
			name: "a malformed line is ignored rather than half-parsed",
			out:  "      current=10.0.0.246\n      current=10.0.0.9|Manual\n",
			want: []adapterAddress{{ip: "10.0.0.9", origin: "Manual"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAdapterAddresses(tt.out)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAdapterAddresses returned %d address(es), want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("address %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// parseAdapterAddresses pulls every "current=<ip>|<origin>" line the
// precondition task printed out of the run's verbose output.
//
// Every address is collected rather than just the first: an adapter can
// hold more than one, and the check that matters ("is the target address
// one this interface already has") is wrong if it looks at only one of
// them.
func parseAdapterAddresses(out string) []adapterAddress {
	var found []adapterAddress
	for _, line := range strings.Split(out, "\n") {
		_, value, ok := strings.Cut(strings.TrimSpace(line), "current=")
		if !ok {
			continue
		}
		ip, origin, ok := strings.Cut(value, "|")
		if !ok {
			continue
		}
		found = append(found, adapterAddress{ip: strings.TrimSpace(ip), origin: strings.TrimSpace(origin)})
	}
	return found
}

// TestWinRMGate_ReachesAWindowsHost is the cheap half, and it runs first
// so a failure here is not mistaken for a failure of the conversion.
//
// It asserts on output the device produced rather than on the exit
// status alone: a run that connected to nothing and a run that connected
// and did nothing both exit zero.
func TestWinRMGate_ReachesAWindowsHost(t *testing.T) {
	cfg := winrmGate(t)
	dir := winrmGateProject(t, cfg)

	// Two runs, because the two things worth proving here need opposite
	// exit statuses and one run cannot have both. The first reads a fact
	// back from the device on the success path; the second proves a
	// remote failure is reported as one rather than swallowed.
	//
	// This used to be a single run whose task exited non-zero on purpose,
	// because a successful task's stdout was not printed by the CLI at
	// all and the error path was the only way to see what the device
	// said. `run --verbose` is what removed the need for that.
	rb := writeRunbook(t, dir, "reach", `id: reach
hosts: win-gate
tasks:
  - name: Identify the host
    exec.winrm.shell:
      shell: powershell
      command: (Get-CimInstance Win32_OperatingSystem).Caption
`)

	out, err := runPleiades(t, dir, "run", rb, "--verbose")
	if err != nil {
		t.Fatalf("the identification runbook failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Windows") {
		t.Errorf("output does not carry the device's own answer, so nothing proves WinRM reached it:\n%s", out)
	}

	failing := writeRunbook(t, dir, "reach_failure", `id: reach_failure
hosts: win-gate
tasks:
  - name: Exit non-zero and prove the status survives the transport
    exec.winrm.shell:
      shell: powershell
      command: exit 3
`)

	out, err = runPleiades(t, dir, "run", failing)
	if err == nil {
		t.Fatalf("a task whose remote script exited 3 reported success:\n%s", out)
	}
	if !strings.Contains(out, "exited 3") {
		t.Errorf("the remote exit status did not survive the transport:\n%s", out)
	}
}

// TestWinRMGate_StaticIPAndBack is the gate proper: convert the adapter
// from DHCP to a static address, prove it took, and convert it back.
//
// The two halves are one test rather than two because the revert must run
// even when the assertion between them fails. Split across two tests, a
// failure in the first would leave the lab machine converted and the
// second might never run at all.
func TestWinRMGate_StaticIPAndBack(t *testing.T) {
	cfg := winrmGate(t)
	dir := winrmGateProject(t, cfg)

	requireSafeToConvert(t, dir, cfg)

	// Registered before the change, so the adapter is put back even if an
	// assertion below fails or the test panics. This is the half the
	// incident that motivated this gate did not have.
	t.Cleanup(func() {
		rb := writeRunbook(t, dir, "revert", `id: revert
hosts: win-gate
tasks:
  - name: Put the adapter back on DHCP
    exec.winrm.shell:
      shell: powershell
      command: |
        $ErrorActionPreference = 'Stop'
        netsh interface ipv4 set address name=`+cfg.adapter+` source=dhcp
        netsh interface ipv4 set dnsservers name=`+cfg.adapter+` source=dhcp
        'reverted to dhcp'
`)
		if out, err := runPleiades(t, dir, "run", rb); err != nil {
			// Loud, because a lab machine has been left converted and
			// somebody has to go put it back by hand.
			t.Errorf("REVERT FAILED: the target is still on a static address and needs attention at the console: %v\n%s", err, out)
		}
	})

	convert := writeRunbook(t, dir, "static", `id: static
hosts: win-gate
tasks:
  - name: Convert the adapter from DHCP to a static address it does not already hold
    exec.winrm.shell:
      shell: powershell
      command: |
        $ErrorActionPreference = 'Stop'
        $current = Get-NetIPAddress -InterfaceAlias `+cfg.adapter+` -AddressFamily IPv4
        if ($current.PrefixOrigin -eq 'Manual' -and $current.IPAddress -eq '`+cfg.ip+`') {
            'unchanged: already static'
            exit 0
        }
        netsh interface ipv4 set address name=`+cfg.adapter+` static `+cfg.ip+` `+cfg.mask+` `+cfg.gateway+`
        netsh interface ipv4 set dnsservers name=`+cfg.adapter+` static `+cfg.dns+` primary
        'converted to static'
`)

	if out, err := runPleiades(t, dir, "run", convert); err != nil {
		t.Fatalf("the conversion runbook failed: %v\n%s", err, out)
	}

	// Read the state back through a FRESH connection, which is the part
	// that proves the box survived losing the one the change arrived on.
	verify := writeRunbook(t, dir, "verify", `id: verify
hosts: win-gate
tasks:
  - name: Report the adapter state
    exec.winrm.shell:
      shell: powershell
      command: |
        $i = Get-NetIPAddress -InterfaceAlias `+cfg.adapter+` -AddressFamily IPv4
        "origin={0} address={1}" -f $i.PrefixOrigin, $i.IPAddress
`)

	out, err := runPleiades(t, dir, "run", verify, "--verbose")
	if err != nil {
		t.Fatalf("could not read the adapter state back after the conversion: %v\n%s", err, out)
	}
	if !strings.Contains(out, "origin=Manual") {
		t.Errorf("the adapter is not statically configured after the conversion:\n%s", out)
	}
	if !strings.Contains(out, "address="+cfg.ip) {
		t.Errorf("the adapter does not carry the expected address %s:\n%s", cfg.ip, out)
	}
}
