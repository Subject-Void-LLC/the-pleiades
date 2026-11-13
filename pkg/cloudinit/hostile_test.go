// The Schema/Injection Hardening table for the NoCloud seed: every field a
// caller sets, given each hostile value, is refused by Validate, or carried
// exactly by the YAML encoder, in its own key only, with no key added.
package cloudinit

import (
	"reflect"
	"sort"
	"testing"
)

// hostile is each value: quotes, a backtick, "$(", ";", a line break, a
// NUL, U+FFFE, a ".." segment, a UNC path, a drive, and YAML's own syntax.
var hostile = []string{"'", `"`, "`id`", "$(id)", ";id", "\n", "\x00", "\uFFFE", "..", `\\server\share`, `C:\`,
	"x\nruncmd: [id]", "x: y", "- x", "&a *a", "!!python/object", "yes", "1e3", "2026-11-13", "#x"}

func TestSeed_HostileValuesAreRefusedOrCarried(t *testing.T) {
	for _, v := range hostile {
		for field, change := range map[string]func(*Seed){
			"instance ID":    func(s *Seed) { s.InstanceID = v },
			"host name":      func(s *Seed) { s.Hostname = v },
			"user name":      func(s *Seed) { s.Login.Username = v },
			"key comment":    func(s *Seed) { s.Login.AuthorizedKey = authorizedKey + v },
			"password hash":  func(s *Seed) { s.Login.PasswordHash = "$6$saltstring$" + v },
			"shell":          func(s *Seed) { s.Shell = "/bin/" + v },
			"package":        func(s *Seed) { s.Packages = []string{v} },
			"interface ID":   func(s *Seed) { s.Interfaces[1].ID = v },
			"interface MAC":  func(s *Seed) { s.Interfaces[1].MAC = v },
			"interface addr": func(s *Seed) { s.Interfaces[1].Address = v },
		} {
			s := lab("pleiades")
			change(&s)
			if s.Validate() != nil {
				for _, doc := range []func() ([]byte, error){s.UserData, s.MetaData, s.NetworkConfig} {
					if out, err := doc(); err == nil || out != nil {
						t.Errorf("%s %q: refused by Validate but a document was written", field, v)
					}
				}
				continue
			}
			checkCarried(t, field+" "+v, s)
		}
	}
}

// checkCarried decodes the seed's three documents and holds each value the
// seed set to its own key, with no key beyond the ones the seed writes.
func checkCarried(t *testing.T, what string, s Seed) {
	t.Helper()
	docs := map[string][]byte{}
	for name, doc := range map[string]func() ([]byte, error){"user-data": s.UserData, "meta-data": s.MetaData, "network-config": s.NetworkConfig} {
		out, err := doc()
		if err != nil {
			t.Fatalf("%s: %s refused a seed Validate accepts: %v", what, name, err)
		}
		docs[name] = out
	}
	user := decode(t, docs["user-data"])
	wantKeys(t, what, "user-data", user, "disable_root", "hostname", "packages", "preserve_hostname", "ssh_pwauth", "users")
	users, _ := user["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("%s: users %v", what, user["users"])
	}
	u, _ := users[0].(map[string]any)
	wantKeys(t, what, "the user", u, "hashed_passwd", "lock_passwd", "name", "shell", "ssh_authorized_keys", "sudo")
	keys, _ := u["ssh_authorized_keys"].([]any)
	shell := s.Shell
	if shell == "" {
		shell = DefaultShell
	}
	if user["hostname"] != s.Hostname || u["name"] != s.Login.Username || u["hashed_passwd"] != s.Login.PasswordHash || u["shell"] != shell ||
		len(keys) != 1 || keys[0] != s.Login.AuthorizedKey {
		t.Errorf("%s: user-data does not carry the seed's values:\n%s", what, docs["user-data"])
	}
	if len(s.Packages) > 0 && !reflect.DeepEqual(user["packages"], []any{s.Packages[0]}) {
		t.Errorf("%s: packages %v, want %q", what, user["packages"], s.Packages)
	}
	meta := decode(t, docs["meta-data"])
	wantKeys(t, what, "meta-data", meta, "instance-id", "local-hostname")
	if meta["instance-id"] != s.InstanceID || meta["local-hostname"] != s.Hostname {
		t.Errorf("%s: meta-data %v", what, meta)
	}
	network := decode(t, docs["network-config"])
	wantKeys(t, what, "network-config", network, "ethernets", "version")
	ethernets, _ := network["ethernets"].(map[string]any)
	if len(ethernets) != len(s.Interfaces) {
		t.Errorf("%s: ethernets %v", what, ethernets)
	}
	for _, i := range s.Interfaces {
		e, _ := ethernets[i.ID].(map[string]any)
		if i.Address != "" && !reflect.DeepEqual(e["addresses"], []any{i.Address}) {
			t.Errorf("%s: interface %s %v", what, i.ID, e)
		}
	}
}

// wantKeys fails unless every key of m is one of allowed.
func wantKeys(t *testing.T, what, doc string, m map[string]any, allowed ...string) {
	t.Helper()
	var extra []string
	for k := range m {
		if sort.SearchStrings(allowed, k) == len(allowed) || allowed[sort.SearchStrings(allowed, k)] != k {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		t.Errorf("%s: %s gained %v", what, doc, extra)
	}
}
