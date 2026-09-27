// Package cloudinit writes the NoCloud seed a new machine's cloud-init
// reads at first boot (user-data, meta-data and network-config), and reads
// back the SSH host keys cloud-init prints on the machine's console.
//
// A seed carries a login's public half only: an authorized key and a
// password hash. It is built from typed values rather than from text a
// runbook supplies, so nothing a caller passes can add a directive, and
// every value is written by a YAML encoder rather than spliced into one.
package cloudinit

import (
	"bytes"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// Login is the account a seed creates or configures.
type Login struct {
	// Username is the account. root is configured rather than created.
	Username string
	// AuthorizedKey is the one authorized_keys line that may log in.
	AuthorizedKey string
	// PasswordHash is a crypt(3) hash for the console, or "" to leave the
	// account with no usable password.
	PasswordHash string
}

// Interface is one network interface, matched by its MAC address.
type Interface struct {
	// ID names it within the seed; it is not the interface's name.
	ID string
	// MAC is its hardware address, as 080027AABBCC or 08:00:27:aa:bb:cc.
	MAC string
	// DHCP asks for an address; otherwise Address is set.
	DHCP bool
	// Address is a static address with its prefix, as 192.168.56.10/24.
	Address string
}

// Seed is everything a NoCloud seed says about one machine.
type Seed struct {
	// InstanceID is unique to this machine's creation: cloud-init runs its
	// first-boot modules again when it changes.
	InstanceID string
	// Hostname is the machine's host name.
	Hostname string
	Login    Login
	// Interfaces are the machine's network interfaces.
	Interfaces []Interface
}

// hostnamePattern is an RFC 1123 host name label.
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// usernamePattern is a login name useradd accepts on Ubuntu.
var usernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// idPattern is an instance or interface ID: letters, digits and . _ -.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// hashPattern is a crypt(3) hash in the $id$... form.
var hashPattern = regexp.MustCompile(`^\$[0-9a-z]+\$[./0-9A-Za-z$=,]+$`)

// Validate refuses a seed that cloud-init would read differently from
// what it says, or that would leave the machine with no way in.
func (s Seed) Validate() error {
	if !idPattern.MatchString(s.InstanceID) {
		return fmt.Errorf("cloudinit: instance ID %q is not letters, digits and . _ -", s.InstanceID)
	}
	if !hostnamePattern.MatchString(s.Hostname) {
		return fmt.Errorf("cloudinit: host name %q is not a valid host name", s.Hostname)
	}
	if !usernamePattern.MatchString(s.Login.Username) {
		return fmt.Errorf("cloudinit: user name %q is not one useradd accepts", s.Login.Username)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s.Login.AuthorizedKey)); err != nil || strings.ContainsAny(s.Login.AuthorizedKey, "\r\n") {
		return fmt.Errorf("cloudinit: the authorized key is not one authorized_keys line")
	}
	if s.Login.PasswordHash != "" && !hashPattern.MatchString(s.Login.PasswordHash) {
		return fmt.Errorf("cloudinit: the password hash is not a crypt(3) hash")
	}
	if len(s.Interfaces) == 0 {
		return fmt.Errorf("cloudinit: a seed needs at least one network interface")
	}
	for _, i := range s.Interfaces {
		if !idPattern.MatchString(i.ID) {
			return fmt.Errorf("cloudinit: interface ID %q is not letters, digits and . _ -", i.ID)
		}
		if _, err := normalMAC(i.MAC); err != nil {
			return err
		}
		if i.DHCP == (i.Address != "") {
			return fmt.Errorf("cloudinit: interface %s needs DHCP or an address, and not both", i.ID)
		}
		if i.Address != "" {
			if _, err := netip.ParsePrefix(i.Address); err != nil {
				return fmt.Errorf("cloudinit: interface %s: address %q is not an address with its prefix, as 192.168.56.10/24", i.ID, i.Address)
			}
		}
	}
	return nil
}

// normalMAC returns mac as cloud-init matches it: lower case, colon
// separated.
func normalMAC(mac string) (string, error) {
	hex := strings.ToLower(strings.ReplaceAll(mac, ":", ""))
	if len(hex) != 12 || strings.Trim(hex, "0123456789abcdef") != "" {
		return "", fmt.Errorf("cloudinit: %q is not a MAC address", mac)
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = hex[2*i : 2*i+2]
	}
	return strings.Join(parts, ":"), nil
}

// userData is the cloud-config document, in the order it is written.
type userData struct {
	Hostname         string `yaml:"hostname"`
	PreserveHostname bool   `yaml:"preserve_hostname"`
	SSHPasswordAuth  bool   `yaml:"ssh_pwauth"`
	DisableRoot      bool   `yaml:"disable_root"`
	Users            []user `yaml:"users"`
}

// user is one entry of cloud-config's users list.
type user struct {
	Name         string   `yaml:"name"`
	LockPassword bool     `yaml:"lock_passwd"`
	HashedPasswd string   `yaml:"hashed_passwd,omitempty"`
	Shell        string   `yaml:"shell,omitempty"`
	Sudo         string   `yaml:"sudo,omitempty"`
	AuthKeys     []string `yaml:"ssh_authorized_keys"`
}

// UserData returns the seed's user-data: the login, and nothing else.
//
// No default user is created, so the only account with a way in is the
// login's. SSH takes no password from anyone; a password, when there is
// one, is for the console. root is allowed to log in by key only when it
// is the login; any other login may use sudo without a password, since
// its key is what proves who it is.
func (s Seed) UserData() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	u := user{
		Name:         s.Login.Username,
		LockPassword: s.Login.PasswordHash == "",
		HashedPasswd: s.Login.PasswordHash,
		AuthKeys:     []string{s.Login.AuthorizedKey},
	}
	if u.Name != "root" {
		u.Shell = "/bin/bash"
		u.Sudo = "ALL=(ALL) NOPASSWD:ALL"
	}
	doc := userData{
		Hostname:    s.Hostname,
		DisableRoot: u.Name != "root",
		Users:       []user{u},
	}
	return document("#cloud-config\n", doc)
}

// MetaData returns the seed's meta-data: the instance ID and host name.
func (s Seed) MetaData() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return document("", struct {
		InstanceID    string `yaml:"instance-id"`
		LocalHostname string `yaml:"local-hostname"`
	}{s.InstanceID, s.Hostname})
}

// ethernet is one network-config version 2 ethernets entry.
type ethernet struct {
	Match     map[string]string `yaml:"match"`
	DHCP4     bool              `yaml:"dhcp4"`
	Addresses []string          `yaml:"addresses,omitempty"`
}

// NetworkConfig returns the seed's network-config, version 2: each
// interface matched by its MAC address, by DHCP or at its address.
func (s Seed) NetworkConfig() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ethernets := map[string]ethernet{}
	for _, i := range s.Interfaces {
		mac, _ := normalMAC(i.MAC)
		e := ethernet{Match: map[string]string{"macaddress": mac}, DHCP4: i.DHCP}
		if i.Address != "" {
			e.Addresses = []string{i.Address}
		}
		ethernets[i.ID] = e
	}
	return document("", struct {
		Version   int                 `yaml:"version"`
		Ethernets map[string]ethernet `yaml:"ethernets"`
	}{2, ethernets})
}

// document encodes v as YAML after header.
func document(header string, v any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("cloudinit: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("cloudinit: %w", err)
	}
	return b.Bytes(), nil
}

// VolumeID is the label cloud-init's NoCloud source looks for.
const VolumeID = "cidata"

// ISO returns the seed as the image NoCloud looks for: an ISO 9660
// filesystem labelled cidata, with Joliet names, holding user-data,
// meta-data and network-config, every date in it modified. It is built
// here rather than on the machine's host, so the host needs no tool to
// make one and only ever receives finished bytes.
func (s Seed) ISO(modified time.Time) ([]byte, error) {
	user, err := s.UserData()
	if err != nil {
		return nil, err
	}
	meta, err := s.MetaData()
	if err != nil {
		return nil, err
	}
	network, err := s.NetworkConfig()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := iso9660.Write(&b, VolumeID, modified, []iso9660.File{
		{Name: "user-data", Data: user},
		{Name: "meta-data", Data: meta},
		{Name: "network-config", Data: network},
	}); err != nil {
		return nil, fmt.Errorf("cloudinit: %w", err)
	}
	return b.Bytes(), nil
}
