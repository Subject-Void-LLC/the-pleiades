// Tests for the vbox sync plugin beyond the shared conformance suite: the
// endpoint it reads, the VMs it quarantines and why, and its refusals,
// against the model VirtualBox host.
package vbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// store is a credential store answering one entry by name.
type store map[string]credential.Credential

func (s store) Lookup(_ context.Context, name string) (credential.Credential, error) {
	if c, ok := s[name]; ok {
		return c, nil
	}
	return credential.Credential{}, credential.ErrNotFound
}

// onModel makes Connect reach model, recording the endpoint and secrets it
// was given.
func onModel(t *testing.T, model *vboxmanagetest.Host) *Endpoint {
	t.Helper()
	var seen Endpoint
	old := HostFunc
	HostFunc = func(e Endpoint, secrets map[string]string) (vboxmanage.Host, error) {
		seen = e
		if secrets[credential.SecretUsername] == "" {
			return vboxmanage.Host{}, errors.New("no username reached the host")
		}
		return model.VBoxHost(), nil
	}
	t.Cleanup(func() { HostFunc = old })
	return &seen
}

func hostCreds() syncplugin.Deps {
	return syncplugin.Deps{Credentials: store{"vbox-host": {Username: "pleiades-gate", Password: "Lab-Passw0rd-1"}}}
}

func config() syncplugin.Config {
	return syncplugin.Config{Name: Name, Endpoint: "https://172.18.32.1", CredentialName: "vbox-host", ReadOnly: true}
}

func TestReadEndpoint(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config()
	cfg.Settings = map[string]string{SettingCAFile: ca, SettingServerName: "vengeance", SettingVBoxManage: `D:\VirtualBox\VBoxManage.exe`}
	e, err := ReadEndpoint(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.Target.Host != "172.18.32.1" || e.Target.Port != 5986 || !e.Options.HTTPS || string(e.Options.CACert) != "-----BEGIN CERTIFICATE-----\n" || e.Options.ServerName != "vengeance" || e.VBoxManage != `D:\VirtualBox\VBoxManage.exe` {
		t.Errorf("read %+v", e)
	}
	cfg = config()
	cfg.Endpoint = "http://192.168.56.1:15985"
	if e, err := ReadEndpoint(cfg); err != nil || e.Target.Port != 15985 || e.Options.HTTPS || e.VBoxManage != DefaultVBoxManage {
		t.Errorf("http with a port: %+v, %v", e, err)
	}
	for why, tt := range map[string]struct {
		endpoint string
		settings map[string]string
		want     string
	}{
		"no endpoint":          {"", nil, "not the host's WinRM address"},
		"another scheme":       {"ssh://172.18.32.1", nil, "not the host's WinRM address"},
		"a path":               {"https://172.18.32.1/wsman", nil, "not the host's WinRM address"},
		"a user":               {"https://admin@172.18.32.1", nil, "not the host's WinRM address"},
		"a query":              {"https://172.18.32.1?x=1", nil, "not the host's WinRM address"},
		"a port out of range":  {"https://172.18.32.1:70000", nil, "no valid port"},
		"a pin over http":      {"http://172.18.32.1", map[string]string{SettingCAFile: ca}, "endpoint is http"},
		"a missing CA file":    {"https://172.18.32.1", map[string]string{SettingCAFile: ca + ".gone"}, "reading ca_file"},
		"a VBoxManage path no": {"https://172.18.32.1", map[string]string{SettingVBoxManage: "VBoxManage.exe\"; calc"}, "VBoxManage"},
	} {
		cfg := config()
		cfg.Endpoint, cfg.Settings = tt.endpoint, tt.settings
		if _, err := ReadEndpoint(cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want an error mentioning %q", why, err, tt.want)
		}
	}
}

func TestClassificationPath(t *testing.T) {
	for osType, want := range map[string]string{
		"Windows Server 2025 (64-bit)": "windows_server",
		"Windows 11 (64-bit)":          "windows_server",
		"Ubuntu (64-bit)":              "linux_server.debian_family",
		"Debian (64-bit)":              "linux_server.debian_family",
		"Red Hat (64-bit)":             "linux_server.rhel_family",
		"Fedora (64-bit)":              "linux_server.rhel_family",
		"Oracle Linux (64-bit)":        "linux_server.rhel_family",
		"Linux 2.6 / 3.x / 4.x":        "linux_server",
		"Other Linux (64-bit)":         "linux_server",
		"FreeBSD (64-bit)":             "",
		"Other/Unknown":                "",
		"":                             "",
	} {
		if got := strings.Join(classificationPath(osType), "."); got != want {
			t.Errorf("%q: %q, want %q", osType, got, want)
		}
	}
}

// TestSync_QuarantinesWhatItCannotReach: a VM with no recorded address
// (a base nobody cloned), one VirtualBox cannot read, and one whose read
// fails each stay visible, quarantined with the reason; a clone with its
// address lands simulate-locked, or active when the source is not read-only.
func TestSync_QuarantinesWhatItCannotReach(t *testing.T) {
	model := vboxmanagetest.New(
		&vboxmanagetest.VM{Name: "win-lab", UUID: "11111111-1111-4111-8111-111111111111", State: vboxmanage.StateRunning, OSType: "Windows Server 2025 (64-bit)",
			Extra: map[string]string{vboxmanage.ExtraAddress: "192.168.56.30", vboxmanage.ExtraDevice: "win-lab"}},
		&vboxmanagetest.VM{Name: "ws2025-base", UUID: "22222222-2222-4222-8222-222222222222", State: vboxmanage.StatePoweroff, OSType: "Windows Server 2025 (64-bit)"},
		&vboxmanagetest.VM{Name: "gone", UUID: "33333333-3333-4333-8333-333333333333", State: vboxmanage.StatePoweroff, Inaccessible: true},
		&vboxmanagetest.VM{Name: "locked", UUID: "44444444-4444-4444-8444-444444444444", State: vboxmanage.StatePoweroff},
	)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe getextradata locked": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	onModel(t, model)
	for _, readOnly := range []bool{true, false} {
		p := New(hostCreds())
		cfg := config()
		cfg.ReadOnly = readOnly
		if err := p.Connect(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		it, err := p.Discover(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]syncplugin.Classification{}
		for it.Next(context.Background()) {
			c, err := p.Classify(context.Background(), it.Record())
			if err != nil {
				t.Fatal(err)
			}
			got[it.Record().Name] = c
		}
		if err := it.Error(); err != nil || it.Close() != nil {
			t.Fatal(err)
		}
		want := inventory.StateSimulateLocked
		if !readOnly {
			want = inventory.StateActive
		}
		if c := got["win-lab"]; c.Type != "windows_server" || c.State != want {
			t.Errorf("read-only %v: win-lab classified %+v", readOnly, c)
		}
		for name, reason := range map[string]string{
			"ws2025-base":                          "records no address",
			"33333333-3333-4333-8333-333333333333": "cannot read this VM's settings",
			"locked":                               "extradata failed",
		} {
			if c := got[name]; !c.Quarantined() || !strings.Contains(c.Reason, reason) {
				t.Errorf("%s: %+v, want quarantined mentioning %q", name, c, reason)
			}
		}
		_ = p.Close()
	}
}

func TestConnect_Refusals(t *testing.T) {
	model := vboxmanagetest.New()
	seen := onModel(t, model)
	if err := New(syncplugin.Deps{}).Connect(context.Background(), config()); err == nil || !strings.Contains(err.Error(), "no credential store") {
		t.Errorf("no store: %v", err)
	}
	cfg := config()
	cfg.CredentialName = ""
	if err := New(hostCreds()).Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), `resolving credential "vbox"`) {
		t.Errorf("the default credential name is the plugin's: %v", err)
	}
	cfg = config()
	cfg.Endpoint = "ftp://x"
	if err := New(hostCreds()).Connect(context.Background(), cfg); err == nil {
		t.Error("a bad endpoint connected")
	}
	if err := New(hostCreds()).Connect(context.Background(), syncplugin.Config{}); err == nil {
		t.Error("a config with no name connected")
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list vms": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	if err := New(hostCreds()).Connect(context.Background(), config()); err == nil || !strings.Contains(err.Error(), "listing the VMs") {
		t.Errorf("a host that refuses the list: %v", err)
	}
	if seen.Target.Host != "172.18.32.1" {
		t.Errorf("the host was reached at %+v", seen.Target)
	}
	old := HostFunc
	HostFunc = func(Endpoint, map[string]string) (vboxmanage.Host, error) {
		return vboxmanage.Host{}, errors.New("no way there")
	}
	defer func() { HostFunc = old }()
	if err := New(hostCreds()).Connect(context.Background(), config()); err == nil || !strings.Contains(err.Error(), "no way there") {
		t.Errorf("a host that cannot be built: %v", err)
	}
}

// TestHostFunc_BuildsAWinRMHost: the real HostFunc reaches the endpoint
// over WinRM with the credential, and refuses secrets with no way to log
// in, before anything is dialed.
func TestHostFunc_BuildsAWinRMHost(t *testing.T) {
	e, err := ReadEndpoint(config())
	if err != nil {
		t.Fatal(err)
	}
	h, err := HostFunc(e, credential.Flatten(credential.Credential{Username: "pleiades-gate", Password: "Lab-Passw0rd-1"}))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := h.Runner.(vboxmanage.WinRMRunner)
	if !ok || r.Target != e.Target || r.Auth.Username != "pleiades-gate" || h.Path != DefaultVBoxManage {
		t.Errorf("built %+v", h)
	}
	if _, err := HostFunc(e, map[string]string{}); err == nil {
		t.Error("a host was built with no credential")
	}
}

func TestNotConnected(t *testing.T) {
	p := New(hostCreds())
	if _, err := p.Discover(context.Background()); !errors.Is(err, syncplugin.ErrNotConnected) {
		t.Errorf("discover: %v", err)
	}
	if _, err := p.Sync(context.Background(), nil); !errors.Is(err, syncplugin.ErrNotConnected) {
		t.Errorf("sync: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Error(err)
	}
}

// TestDiscover_TheListFails: a host that answers Connect's list and then
// refuses the next is an error from Discover, not an empty fleet.
func TestDiscover_TheListFails(t *testing.T) {
	model := vboxmanagetest.New()
	onModel(t, model)
	p := New(hostCreds())
	if err := p.Connect(context.Background(), config()); err != nil {
		t.Fatal(err)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list vms": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	if _, err := p.Discover(context.Background()); err == nil {
		t.Error("a refused list discovered nothing without an error")
	}
}
