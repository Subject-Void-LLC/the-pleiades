// Package bsdinstall writes the script FreeBSD's installer, bsdinstall,
// runs with no one at the keyboard (installerconfig), the small ISO image
// that carries it to a VirtualBox guest as a DVD, and the keystrokes that
// start it.
//
// bsdinstall runs a script without asking only when one sits at
// /etc/installerconfig on the medium it booted from
// (usr.sbin/bsdinstall/startbsdinstall in FreeBSD's source), and a
// released DVD holds none. Rather than rebuild the DVD, the script arrives
// on a second one labeled VolumeID, and the installer is told to run it
// from the shell its Welcome dialog offers: ShellKeys choose it once
// WelcomeOn sees the dialog on the screen, and ScriptCommand runs the
// script there, saying so on the serial port (Started).
//
// The script installs FreeBSD from the DVD itself, with no network, onto
// the machine's first disk with bsdinstall's default partitioning, and
// prepares the installation to be a base clones are made from:
//
//   - sshd and nuageinit enabled, and /firstboot present, so each clone's
//     first boot reads its own NoCloud seed (a login, a host name, an
//     address) the way cloud-init configures an Ubuntu clone;
//   - the serial console on beside the screen, so the host can read what
//     the guest prints;
//   - a first-boot script printing the SSH host keys on the console between
//     the markers cloud-init uses (cloudinit.HostKeysBegin and
//     HostKeysEnd), so cloudinit.HostKeys reads a clone's keys from the
//     hypervisor rather than taking them on first connection.
//
// The typed command powers the machine off once the script succeeds, and
// leaves it running at a shell when it does not, so a failed install is
// there to be looked at.
package bsdinstall

import (
	"bytes"
	"fmt"
	"image/png"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cloudinit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// FileName is the script's name on the DVD this package writes.
const FileName = "installerconfig"

// VolumeID is the label of the DVD this package writes. FreeBSD names a
// labeled ISO 9660 volume /dev/iso9660/<label>, which is how StartKeys
// finds it whichever drive it is in.
const VolumeID = "PLEIADES"

// installerBlue is the background bsddialog draws the installer on, as a
// VirtualBox screenshot renders VGA text mode's blue. The boot loader's
// menu and the kernel's messages are on black, so the installer is the
// first screen that is mostly this color.
var installerBlue = [3]uint32{0, 0, 168}

// blueTolerance is how far a pixel's channel may be from installerBlue
// and still count, for a renderer that rounds differently.
const blueTolerance = 24

// WelcomeOn reports whether a screenshot, a PNG, shows the installer's
// screen: at least half of it installerBlue. Measured on the lab host with
// FreeBSD 15.1's DVD, the Welcome dialog's screen is 82 percent that blue
// and the loader's and the kernel's screens hold none of it.
//
// The screen is what is watched because a DVD booted with BIOS writes
// nothing to the serial console until something asks it to, so the
// console cannot say the dialog is up.
func WelcomeOn(screenshot []byte) (bool, error) {
	img, err := png.Decode(bytes.NewReader(screenshot))
	if err != nil {
		return false, fmt.Errorf("bsdinstall: the screenshot is not a PNG: %w", err)
	}
	bounds := img.Bounds()
	blue := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if near(r>>8, installerBlue[0]) && near(g>>8, installerBlue[1]) && near(b>>8, installerBlue[2]) {
				blue++
			}
		}
	}
	return blue*2 >= bounds.Dx()*bounds.Dy() && blue > 0, nil
}

// near reports whether a channel is within blueTolerance of want.
func near(got, want uint32) bool {
	if got > want {
		return got-want <= blueTolerance
	}
	return want-got <= blueTolerance
}

// Started is the line ScriptCommand writes to the serial port before it
// runs the script: the guest's own word that the keys landed and the
// script's DVD mounted. The installer's log follows it there, so the
// host's copy of the serial console says how far an install got.
const Started = "pleiades: installerconfig started"

// StartedOn reports whether a serial console log holds Started.
func StartedOn(console []byte) bool {
	return strings.Contains(cloudinit.ConsoleText(string(console)), Started)
}

// DefaultDistributions are the distribution sets installed when none are
// named: the kernel and the base system, which is what sshd, nuageinit
// and the file methods need.
var DefaultDistributions = []string{"kernel.txz", "base.txz"}

// Install is an unattended installation from FreeBSD's DVD onto a BIOS
// machine's first disk, prepared as a base to clone.
type Install struct {
	// Distributions are the distribution sets to install from the DVD;
	// empty means DefaultDistributions.
	Distributions []string
}

// distributionName is a distribution set's file name as the DVD's
// /usr/freebsd-dist holds them: base.txz, kernel.txz, lib32.txz.
var distributionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.txz$`)

// Validate refuses an installation bsdinstall would not carry out as
// written.
func (i Install) Validate() error {
	for _, d := range i.Distributions {
		if !distributionName.MatchString(d) {
			return fmt.Errorf("bsdinstall: %q is not a distribution set name such as base.txz", d)
		}
	}
	return nil
}

// Script returns installerconfig: the preamble bsdinstall reads for what
// to install, then the shell script it runs inside the new system.
func (i Install) Script() ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	dists := i.Distributions
	if len(dists) == 0 {
		dists = DefaultDistributions
	}
	var b bytes.Buffer
	// The preamble: bsdinstall's default partitioning of the first disk it
	// finds, the sets to extract from the DVD, and no questions.
	fmt.Fprintf(&b, "PARTITIONS=DEFAULT\nDISTRIBUTIONS=%q\nexport nonInteractive=\"YES\"\n\n", strings.Join(dists, " "))
	// The setup script, run chrooted in the new system after extraction.
	b.WriteString(setup)
	return b.Bytes(), nil
}

// setup is the part of the script that runs in the new system. Every
// value in it is fixed, so nothing a caller passes reaches a shell.
const setup = `#!/bin/sh
set -e
sysrc sshd_enable="YES"
sysrc nuageinit_enable="YES"
sysrc ifconfig_DEFAULT="DHCP"
sysrc pleiades_hostkeys_enable="YES"
touch /firstboot
cat >> /boot/loader.conf <<'LOADER'
autoboot_delay="3"
boot_multicons="YES"
console="comconsole,vidconsole"
LOADER
mkdir -p /usr/local/etc/rc.d
cat > /usr/local/etc/rc.d/pleiades_hostkeys <<'RC'
#!/bin/sh
# PROVIDE: pleiades_hostkeys
# REQUIRE: sshd
# KEYWORD: firstboot
. /etc/rc.subr
name="pleiades_hostkeys"
rcvar="pleiades_hostkeys_enable"
start_cmd="pleiades_hostkeys_start"
stop_cmd=":"
pleiades_hostkeys_start()
{
	{
		echo "` + cloudinit.HostKeysBegin + `"
		cat /etc/ssh/ssh_host_*_key.pub
		echo "` + cloudinit.HostKeysEnd + `"
	} > /dev/console
}
load_rc_config $name
run_rc_command "$1"
RC
chmod 0555 /usr/local/etc/rc.d/pleiades_hostkeys
`

// ISO returns the script as a DVD image labeled VolumeID holding FileName,
// every date in it modified.
func (i Install) ISO(modified time.Time) ([]byte, error) {
	script, err := i.Script()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := iso9660.Write(&b, VolumeID, modified, []iso9660.File{{Name: FileName, Data: script}}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ShellKeys choose the Welcome dialog's Shell button, the one between
// Install and Live System, in the form Packer's boot_command uses.
const ShellKeys = "<right><enter>"

// ScriptCommand is what to type at that shell, once it has started: mount
// this package's DVD by its label, write Started to the serial port, copy
// the installer's log there as it grows, run the script, and power the
// machine off once it succeeds.
const ScriptCommand = "mkdir -p /tmp/pleiades && mount_cd9660 /dev/iso9660/" + VolumeID + " /tmp/pleiades && " +
	"echo '" + Started + "' > /dev/cuau0 && { tail -F /tmp/bsdinstall_log > /dev/cuau0 & } && " +
	"bsdinstall script /tmp/pleiades/" + FileName + " && poweroff<enter>"
