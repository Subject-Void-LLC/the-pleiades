// Pure tests of the login file as data, and of writing it through the
// engine host's files as login does, on this machine's own files.
package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestAuthFile_KeepsWhatItDoesNotChange: setting and removing one login
// leaves every other key and login as it was.
func TestAuthFile_KeepsWhatItDoesNotChange(t *testing.T) {
	f, err := parseAuthFile([]byte(`{"auths":{"ghcr.io":{"auth":"Ym90Ong=","identitytoken":"t"}},"proxies":{"default":{"httpProxy":"p"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	f.set("registry.example.com", encodeAuth("deployer", "pw"))
	data, err := f.bytes()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Auths   map[string]map[string]string `json:"auths"`
		Proxies map[string]map[string]string `json:"proxies"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Auths["ghcr.io"]["identitytoken"] != "t" || out.Proxies["default"]["httpProxy"] != "p" {
		t.Errorf("other keys changed: %s", data)
	}
	if got := out.Auths["registry.example.com"]["auth"]; got != base64.StdEncoding.EncodeToString([]byte("deployer:pw")) {
		t.Errorf("the new login's auth = %q", got)
	}
	if !strings.HasPrefix(string(data), "{\n\t\"auths\"") || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("the file is not written as the engines' tools write it: %q", data)
	}
	if !f.remove("ghcr.io") || f.remove("ghcr.io") {
		t.Error("remove did not report what was there")
	}
	if keys := f.keys(); len(keys) != 1 || keys[0] != "registry.example.com" {
		t.Errorf("keys = %v", keys)
	}
}

// TestAuthFile_ParsesAndRefuses: an empty file is empty; a file that is
// not an object, or whose auths is not one, is refused without quoting it.
func TestAuthFile_ParsesAndRefuses(t *testing.T) {
	for _, empty := range []string{"", "  \n", `{}`, `{"auths":null}`} {
		f, err := parseAuthFile([]byte(empty))
		if err != nil || len(f.keys()) != 0 {
			t.Errorf("parseAuthFile(%q) = %v, %v", empty, f, err)
		}
	}
	for _, bad := range []string{"secret-pw", `["x"]`, `{"auths":"secret-pw"}`} {
		if _, err := parseAuthFile([]byte(bad)); err == nil || strings.Contains(err.Error(), "secret-pw") {
			t.Errorf("parseAuthFile(%q) = %v", bad, err)
		}
	}
}

// TestHelpersKeysAndNames: a credential helper is found per registry and
// for the whole file; Docker Hub is filed under Docker's old index key for
// Docker only; an auth value names its account, or nothing.
func TestHelpersKeysAndNames(t *testing.T) {
	f, _ := parseAuthFile([]byte(`{"credHelpers":{"gcr.io":"gcloud"}}`))
	if f.helperFor("gcr.io") != "gcloud" || f.helperFor("ghcr.io") != "" {
		t.Error("credHelpers is not read per registry")
	}
	f, _ = parseAuthFile([]byte(`{"credsStore":"desktop"}`))
	if f.helperFor("ghcr.io") != "desktop" {
		t.Error("credsStore is not read")
	}
	if configKey(capability.EngineDocker, "docker.io") != dockerHubKey || configKey(capability.EnginePodman, "docker.io") != "docker.io" || configKey(capability.EngineDocker, "ghcr.io") != "ghcr.io" {
		t.Error("configKey files Docker Hub wrongly")
	}
	if username(encodeAuth("deployer", "a:b")) != "deployer" || username("!!") != "" || username(base64.StdEncoding.EncodeToString([]byte("nocolon"))) != "" {
		t.Error("username does not decode an auth value")
	}
}

// TestRegistryHost normalizes Docker Hub's names and refuses anything that
// is not a host.
func TestRegistryHost(t *testing.T) {
	for in, want := range map[string]string{"index.docker.io": "docker.io", "https://GHCR.io/": "ghcr.io", "localhost:5000": "localhost:5000", "10.0.0.1:443": "10.0.0.1:443"} {
		if got, err := registryHost(map[string]any{paramRegistry: in}); err != nil || got != want {
			t.Errorf("registryHost(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "ghcr.io/org", "-x", "a b", "x:", "host:123456"} {
		if _, err := registryHost(map[string]any{paramRegistry: bad}); err == nil {
			t.Errorf("registryHost(%q) was accepted", bad)
		}
	}
}

// localHost is a device whose engine is reached through this machine's
// own socket, so its files are this machine's.
type localHost struct {
	*inventorytest.Stub
}

func (localHost) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EnginePodman}
}
func (localHost) EngineSocket(capability.EngineKind) capability.SocketAddress { return "/s" }
func (localHost) EngineID(capability.EngineKind) capability.EngineID          { return "id" }
func (localHost) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (localHost) EngineLoginUser() string { return "me" }

// TestLoginFile_WritesPrivately: the login file login writes, through the
// engine host's files, is mode 0600 under a directory made 0700, and holds
// what was set; a Podman host's default is under its home's
// .config/containers.
func TestLoginFile_WritesPrivately(t *testing.T) {
	containerengine.AllowLocalCarrier()
	device := localHost{&inventorytest.Stub{StubName: "box", StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameContainerEngine, capability.NamePodman, capability.NameEngineLocal}}}
	p := filepath.Join(t.TempDir(), "containers", "auth.json")
	l, err := openLoginFile(context.Background(), nil, device, map[string]any{paramAuthFile: p}, "x.y")
	if err != nil {
		t.Fatal(err)
	}
	defer l.files.Close()
	if l.kind != capability.EnginePodman || l.path != p {
		t.Fatalf("login file = %s %s", l.kind, l.path)
	}
	l.file.set("ghcr.io", encodeAuth("bot", "pw"))
	if err := l.write(context.Background()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{p: 0o600, filepath.Dir(p): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s's mode = %v, %v; want %o", path, info.Mode(), err, want)
		}
	}
	again, err := openLoginFile(context.Background(), nil, device, map[string]any{paramAuthFile: p}, "x.y")
	if err != nil {
		t.Fatal(err)
	}
	defer again.files.Close()
	if auth, ok := again.file.entry("ghcr.io"); !ok || username(auth) != "bot" {
		t.Errorf("the written login reads back as %q, %v", auth, ok)
	}
	home, _ := os.UserHomeDir()
	def, err := openLoginFile(context.Background(), nil, device, map[string]any{}, "x.y")
	if err != nil {
		t.Fatal(err)
	}
	defer def.files.Close()
	if def.path != home+"/.config/containers/auth.json" {
		t.Errorf("Podman's default login file = %s", def.path)
	}
}
