// Tests and a fuzz target for registry host matching and the X-Registry-Auth
// encoding.
package containerengine

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/moby/moby/api/types/registry"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// regRC answers for one registry.
type regRC struct{ cred *wire.RegistryCredential }

func (regRC) InjectSecrets() map[string]string { return nil }
func (regRC) SetStat(string, any) error        { return nil }
func (regRC) EmitFact(string, any) error       { return nil }
func (r regRC) RegistryCredential(host string) (string, string, bool) {
	return r.cred.For(host)
}

// TestRegistryHostOf reads the registry an image reference names, as the
// engine does.
func TestRegistryHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"alpine":              "docker.io",
		"library/alpine:3.20": "docker.io",
		"ghcr.io/org/app:1":   "ghcr.io",
		"localhost:5000/app":  "localhost:5000",
		"registry.example.com:5000/a/b@sha256:" + sixtyFourHex: "registry.example.com:5000",
	} {
		got, err := RegistryHostOf(in)
		if err != nil || got != want {
			t.Errorf("RegistryHostOf(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := RegistryHostOf("Not A Reference"); err == nil {
		t.Error("an invalid reference was accepted")
	}
}

const sixtyFourHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestValidImage admits the references an engine accepts and refuses every
// hostile value a command line would read as an option or a shell would
// act on.
func TestValidImage(t *testing.T) {
	for _, ok := range []string{
		"alpine", "nginx:1.27", "library/alpine:3.20", "ghcr.io/org/app:1", "localhost:5000/app",
		"registry.example.com:5000/a/b@sha256:" + sixtyFourHex, "alpine@sha256:" + sixtyFourHex,
	} {
		if err := ValidImage(ok); err != nil {
			t.Errorf("ValidImage(%q) = %v, want it accepted", ok, err)
		}
	}
	for _, bad := range []string{
		"", "-", "--privileged", "-v=/:/host", "--network=host", "-it",
		"alpine;id", "alpine && id", "$(id)", "`id`", "alpine id", "alpine\nid", "Alpine",
		"alpine:", "@sha256:" + sixtyFourHex, "alpine\x00",
	} {
		if err := ValidImage(bad); err == nil {
			t.Errorf("ValidImage(%q) accepted a hostile reference", bad)
		}
	}
}

// TestRegistryAuth encodes the account for the image's own registry and
// nothing for any other.
func TestRegistryAuth(t *testing.T) {
	rc := regRC{cred: &wire.RegistryCredential{Host: "ghcr.io", Username: "u", Password: "p"}}
	got, err := RegistryAuth(rc, "ghcr.io/org/app:1")
	if err != nil || got == "" {
		t.Fatalf("RegistryAuth(own registry) = %q, %v", got, err)
	}
	raw, err := base64.URLEncoding.DecodeString(got)
	if err != nil {
		t.Fatal(err)
	}
	var auth registry.AuthConfig
	if err := json.Unmarshal(raw, &auth); err != nil || auth.Username != "u" || auth.Password != "p" || auth.ServerAddress != "ghcr.io" {
		t.Errorf("decoded %+v, %v", auth, err)
	}
	if got, _ := RegistryAuth(rc, "alpine"); got != "" {
		t.Errorf("an image on Docker Hub got ghcr.io's account: %q", got)
	}
	if got, _ := RegistryAuth(liveless{}, "ghcr.io/org/app:1"); got != "" {
		t.Errorf("a run with no registry accounts produced %q", got)
	}
}

// liveless is a run that keeps no registry accounts.
type liveless struct{}

func (liveless) InjectSecrets() map[string]string { return nil }
func (liveless) SetStat(string, any) error        { return nil }
func (liveless) EmitFact(string, any) error       { return nil }

// FuzzRegistryAuth proves the X-Registry-Auth encoding never panics on any
// account or image reference, round-trips every account it encodes exactly,
// and hands an account only to the registry it names: an image whose
// registry is not the credential's gets no header at all.
func FuzzRegistryAuth(f *testing.F) {
	f.Add("ghcr.io", "u", "p", "ghcr.io/org/app:1")
	f.Add("docker.io", "user", "p@ss:w\"rd", "alpine")
	f.Add("localhost:5000", "", "", "localhost:5000/a@sha256:"+sixtyFourHex)
	f.Add("registry.example.com", "u\x00", "\xff\xfe", "registry.example.com/x")
	f.Fuzz(func(t *testing.T, host, user, password, image string) {
		rc := regRC{cred: &wire.RegistryCredential{Host: host, Username: user, Password: password}}
		got, err := RegistryAuth(rc, image)
		if err != nil || got == "" {
			return
		}
		imageHost, herr := RegistryHostOf(image)
		if herr != nil {
			t.Fatalf("RegistryAuth encoded an account for %q, whose registry cannot be read: %v", image, herr)
		}
		if imageHost != wire.RegistryHost(host) {
			t.Fatalf("an account for %q went to an image on %q", host, imageHost)
		}
		raw, err := base64.URLEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("the header is not base64url: %v", err)
		}
		var auth registry.AuthConfig
		if err := json.Unmarshal(raw, &auth); err != nil {
			t.Fatalf("the header is not JSON: %v", err)
		}
		// JSON replaces invalid UTF-8 with U+FFFD, which is a property of
		// the header format rather than of this encoder, so the round trip
		// is checked for valid strings only.
		if utf8.ValidString(user) && utf8.ValidString(password) && (auth.Username != user || auth.Password != password) {
			t.Fatalf("decoded %q/%q, encoded %q/%q", auth.Username, auth.Password, user, password)
		}
	})
}

// registrarRC answers for one registry and keeps what is registered as a
// secret (sdk.SecretRegistrar).
type registrarRC struct {
	regRC
	registered *[]string
}

func (r registrarRC) RegisterSecret(values ...string) {
	*r.registered = append(*r.registered, values...)
}

// TestRegistryAuth_RegistersTheHeaderItBuilds: the header carries the
// password encoded, which a masking set holding the password alone would not
// find, so RegistryAuth registers the value it returns.
func TestRegistryAuth_RegistersTheHeaderItBuilds(t *testing.T) {
	var registered []string
	rc := registrarRC{regRC: regRC{cred: &wire.RegistryCredential{Host: "ghcr.io", Username: "u", Password: "p"}}, registered: &registered}
	header, err := RegistryAuth(rc, "ghcr.io/org/app:1")
	if err != nil || header == "" {
		t.Fatalf("RegistryAuth = %q, %v", header, err)
	}
	if len(registered) != 1 || registered[0] != header {
		t.Fatalf("registered = %v, want the header %q", registered, header)
	}
	if again, err := EncodeRegistryAuth("u", "p", "ghcr.io"); err != nil || again != header {
		t.Errorf("EncodeRegistryAuth = %q, %v; want the same value RegistryAuth built", again, err)
	}
}
