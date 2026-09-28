// The addresses VirtualBox's own DHCP server handed a host-only network's
// machines, read from the leases file it keeps for the account.
package vboxmanage

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// tagLeases is the first line of the script that reads a leases file.
const tagLeases = "# vboxmanage: leases"

// Lease is one address VirtualBox's DHCP server handed out.
type Lease struct {
	// MAC is the adapter's hardware address, 12 upper-case hex digits as
	// showvminfo writes one.
	MAC string
	// Address is the IPv4 address it was given.
	Address string
	// State is the server's word for the lease: acked while it is held,
	// expired after, and offered before the machine took it.
	State string
	// Issued is when it was handed out, and Expires when it runs out.
	Issued, Expires time.Time
}

// leasesFile is a leases file as VirtualBox 7 writes it.
type leasesFile struct {
	Leases []struct {
		MAC     string `xml:"mac,attr"`
		State   string `xml:"state,attr"`
		Address struct {
			Value string `xml:"value,attr"`
		} `xml:"Address"`
		Time struct {
			Issued     int64 `xml:"issued,attr"`
			Expiration int64 `xml:"expiration,attr"`
		} `xml:"Time"`
	} `xml:"Lease"`
}

// ParseLeases reads a leases file. A lease with no MAC or no address is
// refused rather than dropped, since it says the file is not what this
// reads.
func ParseLeases(text string) ([]Lease, error) {
	var f leasesFile
	if err := xml.Unmarshal([]byte(text), &f); err != nil {
		return nil, fmt.Errorf("vboxmanage: the DHCP leases file does not read: %w", err)
	}
	leases := make([]Lease, 0, len(f.Leases))
	for _, l := range f.Leases {
		mac := strings.ToUpper(strings.ReplaceAll(l.MAC, ":", ""))
		if len(mac) != 12 || l.Address.Value == "" {
			return nil, fmt.Errorf("vboxmanage: a DHCP lease names no adapter or no address: %+v", l)
		}
		issued := time.Unix(l.Time.Issued, 0).UTC()
		leases = append(leases, Lease{MAC: mac, Address: l.Address.Value, State: l.State,
			Issued: issued, Expires: issued.Add(time.Duration(l.Time.Expiration) * time.Second)})
	}
	return leases, nil
}

// DHCPLeases returns the leases VirtualBox's DHCP server for the host-only
// adapter holds for this account, none when it has handed out none.
func (h Host) DHCPLeases(ctx context.Context, adapter string) ([]Lease, error) {
	if err := CheckAdapter(adapter); err != nil {
		return nil, err
	}
	script := tagLeases + `
$ErrorActionPreference = 'Stop'
$adapter = '` + adapter + `'
$path = Join-Path $env:USERPROFILE ('.VirtualBox\HostInterfaceNetworking-' + $adapter + '-Dhcpd.leases')
if (-not (Test-Path -LiteralPath $path)) { exit 3 }
[Convert]::ToBase64String([IO.File]::ReadAllBytes($path))
`
	out, err := h.powerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	if out.ExitCode == 3 {
		return nil, nil
	}
	if out.ExitCode != 0 {
		return nil, fmt.Errorf("vboxmanage: reading %s's DHCP leases: %s", adapter, firstErrorLine(out))
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.Stdout))
	if err != nil {
		return nil, fmt.Errorf("vboxmanage: %s's DHCP leases came back unreadable: %w", adapter, err)
	}
	return ParseLeases(string(data))
}
