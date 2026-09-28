// Tests for the seed documents: that each is the YAML cloud-init reads,
// saying only what the seed says, and that a seed which would read
// differently or leave no way in is refused.
package cloudinit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// authorizedKey is a real authorized_keys line.
const authorizedKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGRkT5Qe8cr6G8m3J1W0d8o6xrXEm1yH4r2hH0kRk3p4 root@ubuntu-lab"

// hash is a real SHA-512 crypt hash (openssl passwd -6 -salt saltstring 'Hello world!').
const hash = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"

// lab is the seed of a lab VM with a NAT and a host-only interface.
func lab(username string) Seed {
	return Seed{
		InstanceID: "9883f6c3-b17d-4077-aa52-d9c49a6912a5",
		Hostname:   "ubuntu-lab",
		Login:      Login{Username: username, AuthorizedKey: authorizedKey, PasswordHash: hash},
		Interfaces: []Interface{
			{ID: "nat", MAC: "080027AABBCC", DHCP: true},
			{ID: "hostonly", MAC: "08:00:27:DD:EE:FF", Address: "192.168.56.10/24"},
		},
	}
}

// decode reads a document back as cloud-init would.
func decode(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := yaml.Unmarshal(doc, &v); err != nil {
		t.Fatalf("%s\n%v", doc, err)
	}
	return v
}

func TestUserData_Root(t *testing.T) {
	doc, err := lab("root").UserData()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(doc), "#cloud-config\n") {
		t.Fatalf("user-data does not start with #cloud-config:\n%s", doc)
	}
	v := decode(t, doc)
	users := v["users"].([]any)
	root := users[0].(map[string]any)
	if v["disable_root"] != false || v["ssh_pwauth"] != false || v["hostname"] != "ubuntu-lab" || len(users) != 1 {
		t.Errorf("user-data:\n%s", doc)
	}
	if root["name"] != "root" || root["lock_passwd"] != false || root["hashed_passwd"] != hash || root["sudo"] != nil {
		t.Errorf("root entry: %v", root)
	}
	if keys := root["ssh_authorized_keys"].([]any); len(keys) != 1 || keys[0] != authorizedKey {
		t.Errorf("keys: %v", keys)
	}
}

func TestUserData_AnotherUser(t *testing.T) {
	seed := lab("pleiades")
	seed.Login.PasswordHash = ""
	doc, err := seed.UserData()
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, doc)
	u := v["users"].([]any)[0].(map[string]any)
	if v["disable_root"] != true || u["sudo"] != "ALL=(ALL) NOPASSWD:ALL" || u["shell"] != "/bin/bash" || u["lock_passwd"] != true {
		t.Errorf("user-data:\n%s", doc)
	}
	if _, ok := u["hashed_passwd"]; ok {
		t.Error("a login with no password was given one")
	}
}

// TestUserData_AShellTheGuestHas: a FreeBSD clone's login gets the shell
// its base system has, since nuageinit reads this same seed and FreeBSD
// has no bash; root's own entry names no shell whatever is set.
func TestUserData_AShellTheGuestHas(t *testing.T) {
	seed := lab("pleiades")
	seed.Shell = "/bin/sh"
	doc, err := seed.UserData()
	if err != nil {
		t.Fatal(err)
	}
	if u := decode(t, doc)["users"].([]any)[0].(map[string]any); u["shell"] != "/bin/sh" {
		t.Errorf("user-data:\n%s", doc)
	}
	root := lab("root")
	root.Shell = "/bin/sh"
	doc, err = root.UserData()
	if err != nil {
		t.Fatal(err)
	}
	if u := decode(t, doc)["users"].([]any)[0].(map[string]any); u["shell"] != nil {
		t.Errorf("root was given a shell:\n%s", doc)
	}
}

func TestMetaDataAndNetworkConfig(t *testing.T) {
	seed := lab("root")
	meta, err := seed.MetaData()
	if err != nil {
		t.Fatal(err)
	}
	if v := decode(t, meta); v["instance-id"] != seed.InstanceID || v["local-hostname"] != "ubuntu-lab" {
		t.Errorf("meta-data:\n%s", meta)
	}
	net, err := seed.NetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, net)
	eth := v["ethernets"].(map[string]any)
	nat := eth["nat"].(map[string]any)
	hostonly := eth["hostonly"].(map[string]any)
	if v["version"] != 2 || nat["dhcp4"] != true || nat["match"].(map[string]any)["macaddress"] != "08:00:27:aa:bb:cc" {
		t.Errorf("network-config:\n%s", net)
	}
	if hostonly["dhcp4"] != false || hostonly["addresses"].([]any)[0] != "192.168.56.10/24" || hostonly["match"].(map[string]any)["macaddress"] != "08:00:27:dd:ee:ff" {
		t.Errorf("network-config:\n%s", net)
	}
}

func TestValidate(t *testing.T) {
	for name, change := range map[string]func(*Seed){
		"an instance ID with a slash":    func(s *Seed) { s.InstanceID = "a/b" },
		"a host name with an underscore": func(s *Seed) { s.Hostname = "ubuntu_lab" },
		"a user name with a capital":     func(s *Seed) { s.Login.Username = "Root" },
		"a user name with a newline":     func(s *Seed) { s.Login.Username = "root\nruncmd" },
		"a key with a second line":       func(s *Seed) { s.Login.AuthorizedKey += "\nssh-rsa AAAA" },
		"no key":                         func(s *Seed) { s.Login.AuthorizedKey = "" },
		"a plain password for a hash":    func(s *Seed) { s.Login.PasswordHash = "hunter2" },
		"no interfaces":                  func(s *Seed) { s.Interfaces = nil },
		"an interface ID with a space":   func(s *Seed) { s.Interfaces[0].ID = "n t" },
		"a short MAC":                    func(s *Seed) { s.Interfaces[0].MAC = "0800" },
		"a MAC with a letter past f":     func(s *Seed) { s.Interfaces[0].MAC = "08002700000g" },
		"DHCP and an address":            func(s *Seed) { s.Interfaces[0].Address = "10.0.0.2/24" },
		"neither DHCP nor an address":    func(s *Seed) { s.Interfaces[1].Address = "" },
		"an address with no prefix":      func(s *Seed) { s.Interfaces[1].Address = "192.168.56.10" },
		"a shell that is not a path":     func(s *Seed) { s.Shell = "sh" },
		"a shell with a space":           func(s *Seed) { s.Shell = "/bin/sh -x" },
	} {
		seed := lab("root")
		seed.Interfaces = append([]Interface(nil), seed.Interfaces...)
		change(&seed)
		if err := seed.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
		for doc, fn := range map[string]func() ([]byte, error){"user-data": seed.UserData, "meta-data": seed.MetaData, "network-config": seed.NetworkConfig} {
			if _, err := fn(); err == nil {
				t.Errorf("%s: %s written", name, doc)
			}
		}
	}
}

func TestHostKeys(t *testing.T) {
	console, err := os.ReadFile("testdata/console-ubuntu-2404.log")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := HostKeys(string(console))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || !strings.HasPrefix(keys[0], "ecdsa-sha2-nistp256 AAAA") || !strings.HasPrefix(keys[1], "ssh-ed25519 AAAA") || !strings.HasPrefix(keys[2], "ssh-rsa AAAA") {
		t.Fatalf("keys = %q", keys)
	}
	for _, k := range keys {
		if strings.Count(k, " ") != 1 || strings.ContainsAny(k, "\r\n") {
			t.Errorf("key %q is not algorithm and base64 alone", k)
		}
	}

	// The last complete block is the machine's.
	again := string(console) + strings.Replace(string(console), keys[0], "", 1)
	if newer, err := HostKeys(again); err != nil || len(newer) != 2 {
		t.Errorf("a second boot's block: %q, %v", newer, err)
	}
	// A block still being printed is not read.
	partial := string(console)[:strings.Index(string(console), HostKeysEnd)]
	if _, err := HostKeys(partial); err != ErrNoHostKeys {
		t.Errorf("a partial block: %v", err)
	}
	for name, text := range map[string]string{
		"nothing":        "",
		"an empty block": HostKeysBegin + "\n" + HostKeysEnd + "\n",
		"an end first":   HostKeysEnd + "\n" + HostKeysBegin + "\n",
	} {
		if _, err := HostKeys(text); err != ErrNoHostKeys {
			t.Errorf("%s: %v", name, err)
		}
	}
	broken := HostKeysBegin + "\nssh-ed25519 AAAAnotbase64ofakey\n" + HostKeysEnd + "\n"
	if _, err := HostKeys(broken); err == nil || err == ErrNoHostKeys {
		t.Errorf("a key that does not parse: %v", err)
	}
}

// TestISO holds the seed image to what cloud-init's NoCloud source looks
// for, read by blkid, the tool it finds the seed with: an ISO 9660
// filesystem labelled cidata, holding the three documents.
func TestISO(t *testing.T) {
	seed := lab("root")
	image, err := seed.ISO(time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []func() ([]byte, error){seed.UserData, seed.MetaData, seed.NetworkConfig} {
		want, _ := doc()
		if !bytes.Contains(image, want) {
			t.Errorf("the image lacks %q", want[:20])
		}
	}
	again, _ := seed.ISO(time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC))
	if !bytes.Equal(image, again) {
		t.Error("the same seed and date gave two images")
	}
	blkid, err := exec.LookPath("blkid")
	if err != nil {
		t.Skip("blkid is not installed")
	}
	path := filepath.Join(t.TempDir(), "seed.iso")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(blkid, "-p", "-o", "export", path).Output() // #nosec G204 -- blkid by its looked-up path, on a file this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "TYPE=iso9660") || !strings.Contains(string(out), "LABEL=cidata") {
		t.Errorf("blkid says:\n%s", out)
	}
	bad := lab("root")
	bad.Hostname = "no_underscores"
	if _, err := bad.ISO(time.Now()); err == nil {
		t.Error("an invalid seed was written as an image")
	}
}
