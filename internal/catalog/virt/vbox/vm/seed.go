// The seed a clone is given on its DVD drive: a cloud-init NoCloud seed
// for a machine reached over SSH by a key, or a Windows answer file for
// one reached over WinRM by the built-in Administrator's password.
package vm

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cloudinit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// windowsAdministrator is the account a Windows clone's password is for.
// Its answer file sets the built-in Administrator's, which Windows
// Server's first boot asks for and which remote access admits without
// the token filtering another administrator gets.
const windowsAdministrator = "Administrator"

// seedLogin is the login a clone is seeded with, as the engine derived it
// from the login device's credential.
type seedLogin struct {
	// linux is a key, and a password hash, for a machine reached over SSH.
	linux cloudinit.Login
	// password is the Administrator's password for a Windows machine, or
	// "" for one reached over SSH.
	password string
}

// windows reports whether the login is for a Windows machine.
func (l seedLogin) windows() bool { return l.password != "" }

// readSeedLogin reads the login the engine handed the method.
func readSeedLogin(secrets map[string]string) (seedLogin, error) {
	if password := secrets[wire.SecretSeedPassword]; password != "" {
		if user := secrets[wire.SecretSeedUsername]; !strings.EqualFold(user, windowsAdministrator) {
			return seedLogin{}, fmt.Errorf("a Windows VM is seeded with the built-in %s's password, and the login's credential names %q; store it with --username %s", windowsAdministrator, user, windowsAdministrator)
		}
		return seedLogin{password: password}, nil
	}
	return seedLogin{linux: cloudinit.Login{
		Username:      secrets[wire.SecretSeedUsername],
		AuthorizedKey: secrets[wire.SecretSeedAuthorizedKey],
		PasswordHash:  secrets[wire.SecretSeedPasswordHash],
	}}, nil
}

// check validates the seed before anything is made, with stand-ins for
// what only the new VM can say: its UUID and its adapters' addresses.
func (l seedLogin) check(hostname string, address netip.Prefix) error {
	if l.windows() {
		return windowsSeed(l, hostname, "080027000002", address).Validate()
	}
	probe := cloudinit.Seed{InstanceID: "check", Hostname: hostname, Login: l.linux, Interfaces: interfaces("080027000001", "080027000002", address)}
	return probe.Validate()
}

// matches refuses a login of the other kind from the VM being cloned: a
// Windows VM needs a login reached over WinRM, and any other one reached
// over SSH.
func (l seedLogin) matches(source vboxmanage.Machine, login string) error {
	switch {
	case source.Windows() && !l.windows():
		return fmt.Errorf("%q is a Windows VM (%s), so its login device %q must be one reached over WinRM, with the Administrator's password", source.Name, source.OSType, login)
	case !source.Windows() && l.windows():
		return fmt.Errorf("%q is not a Windows VM (%s), so its login device %q must be one reached over SSH, with a key", source.Name, source.OSType, login)
	}
	return nil
}

// slot is where m's seed goes. A Linux clone's goes in a free IDE slot. A
// Windows clone's goes on SATA: an image made on Hyper-V (Microsoft's
// evaluation VHDX is) has no IDE driver running when Windows first looks
// for its answer file, so a DVD on IDE is not seen, measured 2026-09-27;
// Windows boots from SATA, so a DVD there is seen at once.
func (l seedLogin) slot(m vboxmanage.Machine) (vboxmanage.Slot, bool) {
	if l.windows() {
		return m.FreeSATASlot()
	}
	return m.FreeIDESlot()
}

// slotNeed says what kind of free slot slot looks for.
func (l seedLogin) slotNeed() string {
	if l.windows() {
		return "a Windows VM's seed goes on a SATA port with no drive, which virt.vbox.vm.import_disk and install make"
	}
	return "its seed goes in an IDE slot with no drive"
}

// image is the seed for machine m, dated at.
func (l seedLogin) image(m vboxmanage.Machine, c cloneRequest, at time.Time) ([]byte, error) {
	if l.windows() {
		return windowsSeed(l, c.hostname, m.NICs[2].MAC, c.address).ISO(at)
	}
	seed := cloudinit.Seed{InstanceID: m.UUID, Hostname: c.hostname, Login: l.linux, Interfaces: interfaces(m.NICs[1].MAC, m.NICs[2].MAC, c.address)}
	return seed.ISO(at)
}

// windowsSeed is a Windows clone's answer file: its computer name, the
// Administrator's password, and its host-only adapter at address. The NAT
// adapter asks for its address by DHCP.
func windowsSeed(l seedLogin, hostname, hostOnlyMAC string, address netip.Prefix) winunattend.Seed {
	return winunattend.Seed{ComputerName: hostname, AdministratorPassword: l.password,
		Interfaces: []winunattend.Interface{{MAC: hostOnlyMAC, Address: address}}}
}

// interfaces is a Linux clone's two adapters: NAT by DHCP for the
// internet, and host-only at address for the host and Pleiades.
func interfaces(natMAC, hostOnlyMAC string, address netip.Prefix) []cloudinit.Interface {
	return []cloudinit.Interface{
		{ID: "nat", MAC: natMAC, DHCP: true},
		{ID: "hostonly", MAC: hostOnlyMAC, Address: address.String()},
	}
}
