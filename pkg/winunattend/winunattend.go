// Package winunattend writes the answer files Windows Setup reads without
// anyone at the keyboard (Autounattend.xml), and the small ISO image that
// carries one to a VirtualBox guest as a DVD.
//
// Two answer files are written. A Seed is a generalized Windows image's
// first boot: its computer name, its fixed address, its locale and the
// built-in Administrator's password. An Install is a whole installation
// from Microsoft's media that ends generalized and powered off, a base to
// clone a Seed onto; it holds no password anyone keeps.
//
// Both are built from typed values, and every value is written by an XML
// escaper rather than spliced into text, so nothing a caller passes can
// add an element. Windows Setup looks for Autounattend.xml at the root of
// each removable drive at the start of every configuration pass, which is
// how a DVD made here reaches both.
package winunattend

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// FileName is the name Windows Setup looks for at a removable drive's root.
const FileName = "Autounattend.xml"

// VolumeID is the label of the ISO images this package writes.
const VolumeID = "PLEIADES"

// SeedPath is where a clone of a base Install made finds its answer file:
// the root of its one DVD drive, which Windows calls D:.
const SeedPath = `D:\Autounattend.xml`

// DefaultLanguage is the language and locale a file uses when none is set.
const DefaultLanguage = "en-US"

// DefaultTimeZone is the time zone a Seed sets when none is set.
const DefaultTimeZone = "UTC"

// computerNamePattern is a name Windows accepts for a computer: at most 15
// letters, digits and hyphens, not starting or ending with a hyphen.
var computerNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,13}[A-Za-z0-9])?$`)

// languagePattern is a language tag such as en-US or pt-BR.
var languagePattern = regexp.MustCompile(`^[a-z]{2,3}-[A-Z]{2}$`)

// timeZonePattern is a Windows time zone name, as tzutil /l lists one:
// "UTC", "Pacific Standard Time".
var timeZonePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 .+()-]{0,63}$`)

// Interface is a network adapter given a fixed address, found by its MAC.
type Interface struct {
	// MAC is its hardware address, as 080027AABBCC or 08:00:27:aa:bb:cc.
	MAC string
	// Address is its address with its prefix, as 192.168.56.30/24.
	Address netip.Prefix
}

// Seed is a generalized image's first boot.
type Seed struct {
	// ComputerName is the machine's name.
	ComputerName string
	// AdministratorPassword is the built-in Administrator's password.
	AdministratorPassword string
	// Interfaces are the adapters given fixed addresses; every other
	// adapter asks for one by DHCP.
	Interfaces []Interface
	// Language is its language and locale; "" means DefaultLanguage.
	Language string
	// TimeZone is its time zone; "" means DefaultTimeZone.
	TimeZone string
}

// Validate refuses a seed Windows would read differently from what it
// says, or that would leave the machine with no way in.
func (s Seed) Validate() error {
	if !computerNamePattern.MatchString(s.ComputerName) || isDigits(s.ComputerName) {
		return fmt.Errorf("winunattend: computer name %q is not 1 to 15 letters, digits and hyphens, not all digits and not starting or ending with a hyphen", s.ComputerName)
	}
	if err := checkPassword(s.AdministratorPassword); err != nil {
		return err
	}
	if len(s.Interfaces) == 0 {
		return fmt.Errorf("winunattend: a seed needs at least one adapter with a fixed address")
	}
	for _, i := range s.Interfaces {
		if _, err := dashedMAC(i.MAC); err != nil {
			return err
		}
		if !i.Address.IsValid() || !i.Address.Addr().Is4() || i.Address.Bits() < 8 || i.Address.Bits() > 30 {
			return fmt.Errorf("winunattend: adapter %s's address %s is not an IPv4 address with a prefix of 8 to 30 bits", i.MAC, i.Address)
		}
	}
	if _, err := language(s.Language); err != nil {
		return err
	}
	if s.TimeZone != "" && !timeZonePattern.MatchString(s.TimeZone) {
		return fmt.Errorf("winunattend: time zone %q is not a Windows time zone name such as UTC or Pacific Standard Time", s.TimeZone)
	}
	return nil
}

// isDigits reports whether s is all digits, which Windows refuses as a
// computer name.
func isDigits(s string) bool {
	return strings.Trim(s, "0123456789") == ""
}

// checkPassword refuses a password an answer file cannot carry unchanged
// (too long for Windows, invalid UTF-8, or holding a control character,
// which XML cannot represent) and one Windows' default policy refuses:
// shorter than 8 characters, with fewer than three of upper case, lower
// case, digits and other characters, or holding the account's name.
// Setup would otherwise take the file and leave the account without it.
func checkPassword(p string) error {
	if len(p) > 127 || !utf8.ValidString(p) || strings.ContainsFunc(p, unicode.IsControl) {
		return fmt.Errorf("winunattend: the password is longer than 127 characters or holds a character an answer file cannot carry")
	}
	kinds := 0
	for _, is := range []func(rune) bool{unicode.IsUpper, unicode.IsLower, unicode.IsDigit, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}} {
		if strings.ContainsFunc(p, is) {
			kinds++
		}
	}
	if utf8.RuneCountInString(p) < 8 || kinds < 3 || strings.Contains(strings.ToLower(p), "administrator") {
		return fmt.Errorf("winunattend: Windows refuses a password shorter than 8 characters, with fewer than three of upper case, lower case, digits and symbols, or holding the name Administrator")
	}
	return nil
}

// passwordAlphabet is what NewPassword draws from: letters and digits
// without the ones read alike, as the credential vault's own generator.
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// NewPassword returns 24 characters of passwordAlphabet chosen by
// crypto/rand, drawn again until Windows' default policy accepts them.
func NewPassword() (string, error) {
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for {
		b := make([]byte, 24)
		for i := range b {
			n, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", fmt.Errorf("winunattend: making a password: %w", err)
			}
			b[i] = passwordAlphabet[n.Int64()]
		}
		if checkPassword(string(b)) == nil {
			return string(b), nil
		}
	}
}

// language returns lang, or DefaultLanguage when it is empty, refusing
// one that is not a language tag.
func language(lang string) (string, error) {
	if lang == "" {
		return DefaultLanguage, nil
	}
	if !languagePattern.MatchString(lang) {
		return "", fmt.Errorf("winunattend: language %q is not a tag such as en-US", lang)
	}
	return lang, nil
}

// dashedMAC returns mac as an answer file names an adapter by it: upper
// case, dash separated.
func dashedMAC(mac string) (string, error) {
	hex := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(hex) != 12 || strings.Trim(hex, "0123456789ABCDEF") != "" {
		return "", fmt.Errorf("winunattend: %q is not a MAC address", mac)
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = hex[2*i : 2*i+2]
	}
	return strings.Join(parts, "-"), nil
}

// XML returns the seed's answer file: the computer name, time zone and
// fixed addresses in the specialize pass, and the locale, the
// Administrator's password and the OOBE screens skipped in oobeSystem.
// The password is written as it is: an answer file's other form for it is
// base64, which hides it from nobody.
func (s Seed) XML() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	lang, _ := language(s.Language)
	zone := s.TimeZone
	if zone == "" {
		zone = DefaultTimeZone
	}
	var interfaces []*node
	for _, i := range s.Interfaces {
		mac, _ := dashedMAC(i.MAC)
		// Windows reads an Interface's children in this order only.
		interfaces = append(interfaces, el("Interface",
			el("Ipv4Settings", leaf("DhcpEnabled", "false")),
			leaf("Identifier", mac),
			el("UnicastIpAddresses", leaf("IpAddress", i.Address.String()).added().with("wcm:keyValue", "1")),
		).added())
	}
	return document(
		pass("specialize",
			component("Microsoft-Windows-Shell-Setup",
				leaf("ComputerName", s.ComputerName),
				leaf("TimeZone", zone)),
			component("Microsoft-Windows-TCPIP", el("Interfaces", interfaces...))),
		pass("oobeSystem",
			locale("Microsoft-Windows-International-Core", lang),
			component("Microsoft-Windows-Shell-Setup",
				el("OOBE",
					leaf("HideEULAPage", "true"),
					leaf("HideLocalAccountScreen", "true"),
					leaf("HideOnlineAccountScreens", "true"),
					leaf("HideWirelessSetupInOOBE", "true"),
					leaf("ProtectYourPC", "3")),
				el("UserAccounts", password("AdministratorPassword", s.AdministratorPassword)))),
	), nil
}

// locale is an International-Core component setting every locale to lang.
func locale(name, lang string, extra ...*node) *node {
	children := append([]*node{}, extra...)
	children = append(children,
		leaf("InputLocale", lang),
		leaf("SystemLocale", lang),
		leaf("UILanguage", lang),
		leaf("UserLocale", lang))
	return component(name, children...)
}

// password is a password element holding value as it is.
func password(name, value string) *node {
	return el(name, leaf("Value", value), leaf("PlainText", "true"))
}

// ISO returns the seed as a DVD image holding FileName, every date in it
// modified.
func (s Seed) ISO(modified time.Time) ([]byte, error) {
	answer, err := s.XML()
	if err != nil {
		return nil, err
	}
	return image(answer, modified)
}

// image writes answer into an ISO as FileName.
func image(answer []byte, modified time.Time) ([]byte, error) {
	var b bytes.Buffer
	if err := iso9660.Write(&b, VolumeID, modified, []iso9660.File{{Name: FileName, Data: answer}}); err != nil {
		return nil, fmt.Errorf("winunattend: %w", err)
	}
	return b.Bytes(), nil
}

// Install is an unattended installation from Windows' own media onto a
// BIOS machine's first disk, which then goes through audit mode, is
// generalized, and powers itself off.
type Install struct {
	// Image is the edition to install: its name in the media's
	// install.wim, or its index there, 1 for the first.
	Image string
	// Language is the installation's language and locale; "" means
	// DefaultLanguage.
	Language string
	// AuditPassword is the built-in Administrator's password while the
	// installation runs: audit mode signs in with it. Windows keeps no
	// copy once it is generalized, and a Seed sets the next one, so the
	// caller makes it at random and keeps it nowhere.
	AuditPassword string
}

// imageNamePattern is an edition's name as install.wim lists it:
// "Windows Server 2025 Standard Evaluation (Desktop Experience)".
var imageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ().,_-]{0,127}$`)

// Validate refuses an installation Windows Setup would not carry out
// unattended.
func (i Install) Validate() error {
	if !imageNamePattern.MatchString(i.Image) {
		return fmt.Errorf("winunattend: image %q is not an edition's name or index, such as Windows Server 2025 Standard Evaluation or 1", i.Image)
	}
	if n, err := strconv.Atoi(i.Image); err == nil && n < 1 {
		return fmt.Errorf("winunattend: image index %d is not 1 or more", n)
	}
	if _, err := language(i.Language); err != nil {
		return err
	}
	return checkPassword(i.AuditPassword)
}

// XML returns the installation's answer file.
//
// windowsPE partitions the first disk as a BIOS machine boots from it (a
// 300 MB active system partition, then Windows on the rest), installs the
// image and accepts its license, with no update downloaded during setup.
// oobeSystem sends the machine to audit mode rather than the screens a
// person answers; auditSystem signs the built-in Administrator in there,
// with AuditPassword; and auditUser deletes the copy of this file Setup
// cached, points the registry at SeedPath, then runs sysprep to generalize
// the installation and shut the machine down. A generalized image's first
// boot looks for its answer file only in fixed places (the registry
// value first, then Panther), never on a DVD, so without the pointer a
// clone's seed is never read, and left cached this file is found first
// and nothing further is looked at. The Generalize setting, which
// Microsoft documents for the last step, runs sysprep before any command
// in the pass, so every step is a command.
func (i Install) XML() ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	lang, _ := language(i.Language)
	key := "/IMAGE/NAME"
	if isDigits(i.Image) {
		key = "/IMAGE/INDEX"
	}
	return document(
		pass("windowsPE",
			locale("Microsoft-Windows-International-Core-WinPE", lang, el("SetupUILanguage", leaf("UILanguage", lang))),
			component("Microsoft-Windows-Setup",
				el("DiskConfiguration",
					el("Disk",
						el("CreatePartitions",
							el("CreatePartition", leaf("Order", "1"), leaf("Type", "Primary"), leaf("Size", "300")).added(),
							el("CreatePartition", leaf("Order", "2"), leaf("Type", "Primary"), leaf("Extend", "true")).added()),
						el("ModifyPartitions",
							el("ModifyPartition", leaf("Order", "1"), leaf("PartitionID", "1"), leaf("Label", "System"), leaf("Format", "NTFS"), leaf("Active", "true")).added(),
							el("ModifyPartition", leaf("Order", "2"), leaf("PartitionID", "2"), leaf("Label", "Windows"), leaf("Letter", "C"), leaf("Format", "NTFS")).added()),
						leaf("DiskID", "0"),
						leaf("WillWipeDisk", "true")).added()),
				el("DynamicUpdate", leaf("Enable", "false"), leaf("WillShowUI", "Never")),
				el("ImageInstall", el("OSImage",
					el("InstallFrom", el("MetaData", leaf("Key", key), leaf("Value", i.Image)).added()),
					el("InstallTo", leaf("DiskID", "0"), leaf("PartitionID", "2")))),
				el("UserData", leaf("AcceptEula", "true")))),
		pass("oobeSystem",
			component("Microsoft-Windows-Deployment", el("Reseal", leaf("Mode", "Audit")))),
		pass("auditSystem",
			component("Microsoft-Windows-Shell-Setup",
				el("AutoLogon",
					leaf("Enabled", "true"),
					leaf("LogonCount", "1"),
					password("Password", i.AuditPassword),
					leaf("Username", "Administrator")),
				el("UserAccounts", password("AdministratorPassword", i.AuditPassword)))),
		pass("auditUser",
			component("Microsoft-Windows-Deployment",
				el("RunSynchronous",
					// Setup caches this file in Panther, and a clone's first
					// boot finds it there, sees nothing for its own passes, and
					// looks no further, never at the seed on its DVD. The
					// Generalize setting ran sysprep before any command could
					// delete it (measured 2026-09-27), so the two steps are
					// commands, run in this order.
					el("RunSynchronousCommand",
						leaf("Order", "1"),
						leaf("Path", `cmd.exe /c del /f /q "%WINDIR%\Panther\unattend.xml"`),
						leaf("Description", "Forget this install's answer file, so a clone's first boot reads its own")).added(),
					// After a generalized image boots, Setup looks for its
					// answer file only in fixed places, never on removable
					// media (measured 2026-09-27), and the first place it
					// looks is this registry value. A clone's seed is its one
					// DVD, so D:.
					el("RunSynchronousCommand",
						leaf("Order", "2"),
						leaf("Path", `reg.exe add HKLM\SYSTEM\Setup /v UnattendFile /t REG_SZ /d `+SeedPath+` /f`),
						leaf("Description", "Point a clone's first boot at the answer file on its seed DVD")).added(),
					el("RunSynchronousCommand",
						leaf("Order", "3"),
						leaf("Path", `%WINDIR%\System32\Sysprep\sysprep.exe /generalize /oobe /shutdown /quiet`),
						leaf("Description", "Generalize this installation and shut it down")).added()))),
	), nil
}

// ISO returns the installation's answer file as a DVD image holding
// FileName, every date in it modified.
func (i Install) ISO(modified time.Time) ([]byte, error) {
	answer, err := i.XML()
	if err != nil {
		return nil, err
	}
	return image(answer, modified)
}
