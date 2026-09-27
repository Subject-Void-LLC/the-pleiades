// Package winunattend_test holds the answer files to what Windows Setup
// reads: well-formed XML, each setting in its pass and component, the
// adapter's children in the one order Windows accepts, and every value
// escaped rather than spliced.
package winunattend_test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
)

// seed is a valid seed for the lab's Windows VM.
func seed() winunattend.Seed {
	return winunattend.Seed{ComputerName: "win-lab", AdministratorPassword: "Pa55word2x",
		Interfaces: []winunattend.Interface{{MAC: "08:00:27:aa:bb:cc", Address: netip.MustParsePrefix("192.168.56.30/24")}}}
}

// install is a valid installation of Server Core.
func install() winunattend.Install {
	return winunattend.Install{Image: "Windows Server 2025 Standard Evaluation", AuditPassword: "Temp0rary9"}
}

// setting is one element with text, found by its path from the root:
// the pass, the component and the elements under it.
type setting struct {
	path, text string
}

// settings reads every element holding text, each with its path, the
// pass and component named by their attributes.
func settings(t *testing.T, doc []byte) []setting {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	var path []string
	var out []setting
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v\n%s", err, doc)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			name := tok.Name.Local
			for _, a := range tok.Attr {
				if a.Name.Local == "pass" || (a.Name.Local == "name" && name == "component") {
					name = a.Value
				}
			}
			path = append(path, name)
			text.Reset()
		case xml.CharData:
			text.Write(tok)
		case xml.EndElement:
			if s := strings.TrimSpace(text.String()); s != "" {
				out = append(out, setting{strings.Join(path[1:], "/"), s})
			}
			text.Reset()
			path = path[:len(path)-1]
		}
	}
}

// has reports whether doc sets path to text.
func has(got []setting, path, text string) bool {
	for _, s := range got {
		if s.path == path && s.text == text {
			return true
		}
	}
	return false
}

func TestSeedSetsWhatAFirstBootNeeds(t *testing.T) {
	s := seed()
	s.AdministratorPassword = `Aa1<b&c>"d'`
	doc, err := s.XML()
	if err != nil {
		t.Fatal(err)
	}
	got := settings(t, doc)
	for _, want := range []setting{
		{"specialize/Microsoft-Windows-Shell-Setup/ComputerName", "win-lab"},
		{"specialize/Microsoft-Windows-Shell-Setup/TimeZone", "UTC"},
		{"specialize/Microsoft-Windows-TCPIP/Interfaces/Interface/Ipv4Settings/DhcpEnabled", "false"},
		{"specialize/Microsoft-Windows-TCPIP/Interfaces/Interface/Identifier", "08-00-27-AA-BB-CC"},
		{"specialize/Microsoft-Windows-TCPIP/Interfaces/Interface/UnicastIpAddresses/IpAddress", "192.168.56.30/24"},
		{"oobeSystem/Microsoft-Windows-International-Core/UILanguage", "en-US"},
		{"oobeSystem/Microsoft-Windows-Shell-Setup/OOBE/HideEULAPage", "true"},
		{"oobeSystem/Microsoft-Windows-Shell-Setup/OOBE/HideLocalAccountScreen", "true"},
		// The password round-trips through the escaper unchanged.
		{"oobeSystem/Microsoft-Windows-Shell-Setup/UserAccounts/AdministratorPassword/Value", `Aa1<b&c>"d'`},
		{"oobeSystem/Microsoft-Windows-Shell-Setup/UserAccounts/AdministratorPassword/PlainText", "true"},
	} {
		if !has(got, want.path, want.text) {
			t.Errorf("no %s = %q in\n%s", want.path, want.text, doc)
		}
	}
	// Windows reads an adapter's children in this order only.
	text := string(doc)
	if !(strings.Index(text, "<Ipv4Settings>") < strings.Index(text, "<Identifier>") && strings.Index(text, "<Identifier>") < strings.Index(text, "<UnicastIpAddresses>")) {
		t.Errorf("an adapter's children are out of order:\n%s", doc)
	}
	if !strings.Contains(text, `<IpAddress wcm:action="add" wcm:keyValue="1">`) {
		t.Errorf("the address is not a keyed list entry:\n%s", doc)
	}
}

func TestSeedLanguageAndTimeZone(t *testing.T) {
	s := seed()
	s.Language, s.TimeZone = "de-DE", "W. Europe Standard Time"
	doc, err := s.XML()
	if err != nil {
		t.Fatal(err)
	}
	got := settings(t, doc)
	if !has(got, "oobeSystem/Microsoft-Windows-International-Core/InputLocale", "de-DE") ||
		!has(got, "specialize/Microsoft-Windows-Shell-Setup/TimeZone", "W. Europe Standard Time") {
		t.Errorf("language or time zone not set:\n%s", doc)
	}
}

func TestSeedRefusals(t *testing.T) {
	for name, change := range map[string]func(*winunattend.Seed){
		"a name too long":          func(s *winunattend.Seed) { s.ComputerName = "a-name-too-long-for-windows" },
		"a name of digits":         func(s *winunattend.Seed) { s.ComputerName = "1234" },
		"a name with a dot":        func(s *winunattend.Seed) { s.ComputerName = "win.lab" },
		"a name ending in -":       func(s *winunattend.Seed) { s.ComputerName = "win-" },
		"no password":              func(s *winunattend.Seed) { s.AdministratorPassword = "" },
		"a password with a NUL":    func(s *winunattend.Seed) { s.AdministratorPassword = "aB1\x00bbbbb" },
		"a password of bad UTF-8":  func(s *winunattend.Seed) { s.AdministratorPassword = "aB1\xffbbbbb" },
		"a password too long":      func(s *winunattend.Seed) { s.AdministratorPassword = strings.Repeat("aB1", 43) },
		"a short password":         func(s *winunattend.Seed) { s.AdministratorPassword = "aB1aB1a" },
		"two kinds of character":   func(s *winunattend.Seed) { s.AdministratorPassword = "abcdefGHIJKL" },
		"the account's name":       func(s *winunattend.Seed) { s.AdministratorPassword = "MyAdministrator1" },
		"no adapter":               func(s *winunattend.Seed) { s.Interfaces = nil },
		"a bad MAC":                func(s *winunattend.Seed) { s.Interfaces[0].MAC = "08:00:27:aa:bb" },
		"a MAC of letters":         func(s *winunattend.Seed) { s.Interfaces[0].MAC = "08002700ZZZZ" },
		"no address":               func(s *winunattend.Seed) { s.Interfaces[0].Address = netip.Prefix{} },
		"an IPv6 address":          func(s *winunattend.Seed) { s.Interfaces[0].Address = netip.MustParsePrefix("fd00::1/64") },
		"a host prefix":            func(s *winunattend.Seed) { s.Interfaces[0].Address = netip.MustParsePrefix("192.168.56.30/32") },
		"a language that is not":   func(s *winunattend.Seed) { s.Language = "english" },
		"a time zone with a quote": func(s *winunattend.Seed) { s.TimeZone = `UTC"` },
	} {
		s := seed()
		s.Interfaces = append([]winunattend.Interface(nil), s.Interfaces...)
		change(&s)
		if _, err := s.XML(); err == nil {
			t.Errorf("%s: written", name)
		}
		if _, err := s.ISO(time.Now()); err == nil {
			t.Errorf("%s: imaged", name)
		}
	}
}

func TestInstallInstallsGeneralizesAndShutsDown(t *testing.T) {
	doc, err := install().XML()
	if err != nil {
		t.Fatal(err)
	}
	// The cached answer file is deleted before sysprep runs.
	if text := string(doc); strings.Index(text, "del /f /q") > strings.Index(text, "UnattendFile") ||
		strings.Index(text, "UnattendFile") > strings.Index(text, "sysprep.exe") || strings.Contains(text, "<Generalize>") {
		t.Errorf("the audit pass's steps are out of order:\n%s", doc)
	}
	got := settings(t, doc)
	const setup = "windowsPE/Microsoft-Windows-Setup/"
	for _, want := range []setting{
		{"windowsPE/Microsoft-Windows-International-Core-WinPE/SetupUILanguage/UILanguage", "en-US"},
		{setup + "DiskConfiguration/Disk/DiskID", "0"},
		{setup + "DiskConfiguration/Disk/WillWipeDisk", "true"},
		{setup + "DiskConfiguration/Disk/CreatePartitions/CreatePartition/Size", "300"},
		{setup + "DiskConfiguration/Disk/CreatePartitions/CreatePartition/Extend", "true"},
		{setup + "DiskConfiguration/Disk/ModifyPartitions/ModifyPartition/Active", "true"},
		{setup + "DiskConfiguration/Disk/ModifyPartitions/ModifyPartition/Letter", "C"},
		{setup + "DynamicUpdate/Enable", "false"},
		{setup + "ImageInstall/OSImage/InstallFrom/MetaData/Key", "/IMAGE/NAME"},
		{setup + "ImageInstall/OSImage/InstallFrom/MetaData/Value", "Windows Server 2025 Standard Evaluation"},
		{setup + "ImageInstall/OSImage/InstallTo/PartitionID", "2"},
		{setup + "UserData/AcceptEula", "true"},
		{"oobeSystem/Microsoft-Windows-Deployment/Reseal/Mode", "Audit"},
		{"auditSystem/Microsoft-Windows-Shell-Setup/AutoLogon/Username", "Administrator"},
		{"auditSystem/Microsoft-Windows-Shell-Setup/AutoLogon/Password/Value", "Temp0rary9"},
		{"auditSystem/Microsoft-Windows-Shell-Setup/UserAccounts/AdministratorPassword/Value", "Temp0rary9"},
		{"auditUser/Microsoft-Windows-Deployment/RunSynchronous/RunSynchronousCommand/Path", `cmd.exe /c del /f /q "%WINDIR%\Panther\unattend*.xml"`},
		{"auditUser/Microsoft-Windows-Deployment/RunSynchronous/RunSynchronousCommand/Path", `reg.exe add HKLM\SYSTEM\Setup /v UnattendFile /t REG_SZ /d D:\Autounattend.xml /f`},
		{"auditUser/Microsoft-Windows-Deployment/RunSynchronous/RunSynchronousCommand/Path", `%WINDIR%\System32\Sysprep\sysprep.exe /generalize /oobe /shutdown /quiet`},
	} {
		if !has(got, want.path, want.text) {
			t.Errorf("no %s = %q in\n%s", want.path, want.text, doc)
		}
	}
}

func TestInstallByIndex(t *testing.T) {
	i := install()
	i.Image, i.Language = "4", "fr-FR"
	doc, err := i.XML()
	if err != nil {
		t.Fatal(err)
	}
	got := settings(t, doc)
	if !has(got, "windowsPE/Microsoft-Windows-Setup/ImageInstall/OSImage/InstallFrom/MetaData/Key", "/IMAGE/INDEX") ||
		!has(got, "windowsPE/Microsoft-Windows-International-Core-WinPE/UILanguage", "fr-FR") {
		t.Errorf("not installed by index in French:\n%s", doc)
	}
}

func TestInstallRefusals(t *testing.T) {
	for name, change := range map[string]func(*winunattend.Install){
		"no image":             func(i *winunattend.Install) { i.Image = "" },
		"an image with <":      func(i *winunattend.Install) { i.Image = "Windows<Server" },
		"index zero":           func(i *winunattend.Install) { i.Image = "0" },
		"a bad language":       func(i *winunattend.Install) { i.Language = "EN" },
		"no audit password":    func(i *winunattend.Install) { i.AuditPassword = "" },
		"a password with a CR": func(i *winunattend.Install) { i.AuditPassword = "aB1\rbbbbb" },
	} {
		i := install()
		change(&i)
		if _, err := i.XML(); err == nil {
			t.Errorf("%s: written", name)
		}
		if _, err := i.ISO(time.Now()); err == nil {
			t.Errorf("%s: imaged", name)
		}
	}
}

func TestISOsCarryTheAnswerFileUnderItsName(t *testing.T) {
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for name, tt := range map[string]struct {
		iso func() ([]byte, error)
		xml func() ([]byte, error)
	}{
		"seed":    {func() ([]byte, error) { return seed().ISO(at) }, seed().XML},
		"install": {func() ([]byte, error) { return install().ISO(at) }, install().XML},
	} {
		image, err := tt.iso()
		if err != nil {
			t.Fatal(err)
		}
		answer, _ := tt.xml()
		if !bytes.Contains(image, answer) {
			t.Errorf("%s: the image does not hold the answer file", name)
		}
		// The Joliet tree records the name as written, in UTF-16.
		joliet := []byte{0, 'A', 0, 'u', 0, 't', 0, 'o', 0, 'u', 0, 'n', 0, 'a', 0, 't', 0, 't', 0, 'e', 0, 'n', 0, 'd', 0, '.', 0, 'x', 0, 'm', 0, 'l'}
		if !bytes.Contains(image, joliet) || !bytes.Contains(image, []byte(winunattend.VolumeID)) {
			t.Errorf("%s: the image does not name %s under label %s", name, winunattend.FileName, winunattend.VolumeID)
		}
		again, _ := tt.iso()
		if !bytes.Equal(image, again) {
			t.Errorf("%s: the same answer made two different images", name)
		}
	}
}

func TestNewPasswordIsOneWindowsAccepts(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p, err := winunattend.NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		s := seed()
		s.AdministratorPassword = p
		if err := s.Validate(); err != nil || len(p) != 24 || seen[p] {
			t.Fatalf("%q: %v", p, err)
		}
		seen[p] = true
	}
}
