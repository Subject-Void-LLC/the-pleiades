// Package vbox implements the "vbox" inventory sync plugin: PLAN.md
// Section 6a's connector for a VirtualBox host, which knows every VM it
// runs. It reads the host's VMs over WinRM through pkg/vboxmanage, the
// same way the virt.vbox.vm.* methods reach it, and brings each VM into
// inventory as the device that reaches its guest: a Windows VM as a
// windows_server, a Linux one as a linux_server, at the fixed address
// virt.vbox.vm.clone recorded on it.
//
// It never writes to the host, so it is read-only by default and its VMs
// land simulate-locked, as Catalyst Center's devices do. A VM with no
// recorded address, or of an OS type no rule places, is quarantined with
// the reason, never guessed at.
package vbox

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Name is this plugin's registration key and the value stamped into the
// SourceAuthority of every device it syncs.
const Name = "vbox"

// The plugin's settings, beyond Config's shared fields.
const (
	// SettingCAFile is a PEM file holding the authority the host's WinRM
	// certificate is checked against, when it is not one the system trusts.
	SettingCAFile = "ca_file"
	// SettingServerName is the name the host's certificate is checked
	// against, when it does not name the address dialed.
	SettingServerName = "server_name"
	// SettingVBoxManage is where VBoxManage is on the host.
	SettingVBoxManage = "vboxmanage"
)

// DefaultVBoxManage is where VirtualBox's installer puts VBoxManage on a
// Windows host.
const DefaultVBoxManage = `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`

// callTimeout bounds each VBoxManage call, as the vm methods' does.
const callTimeout = 5 * time.Minute

func init() {
	syncplugin.MustRegister(syncplugin.Descriptor{
		Name:        Name,
		Description: "reads the VMs a VirtualBox host runs, over WinRM, as the devices that reach their guests",
		DefaultConfig: syncplugin.Config{
			Name:     Name,
			ReadOnly: true,
		},
		// Proven against the lab's real VirtualBox host through
		// pleiades inventory sync, as well as the conformance suite's
		// model host.
		Status:              syncplugin.StatusImplemented,
		RequiresCredentials: true,
		Settings: []syncplugin.SettingSpec{
			{Name: SettingCAFile, Description: "a PEM file with the authority the host's WinRM certificate is checked against, when the system does not trust it"},
			{Name: SettingServerName, Description: "the name the host's WinRM certificate is checked against, when it does not name the endpoint's host"},
			{Name: SettingVBoxManage, Description: "where VBoxManage is on the host; default " + DefaultVBoxManage},
		},
		New: func(deps syncplugin.Deps) syncplugin.Plugin { return New(deps) },
	})
}

// Endpoint is where and how the host is reached, read from a Config.
type Endpoint struct {
	// Target is the host's WinRM address.
	Target winrmexec.Target
	// Options are its TLS settings and call timeout.
	Options winrmexec.Options
	// VBoxManage is where VBoxManage is on the host.
	VBoxManage string
}

// HostFunc builds the VirtualBox host Connect reaches, from the endpoint
// and the credential's secrets. The conformance suite swaps it for the
// model host (pkg/vboxmanage/vboxmanagetest), which stands in for
// VirtualBox in every vbox test; the real one is proven against the lab
// host through pleiades inventory sync.
var HostFunc = func(e Endpoint, secrets map[string]string) (vboxmanage.Host, error) {
	auth, err := winrmexec.AuthFromSecrets(secrets)
	if err != nil {
		return vboxmanage.Host{}, err
	}
	// Refused here, before anything is dialed, rather than as a failed
	// login on the host.
	if err := auth.Validate(); err != nil {
		return vboxmanage.Host{}, err
	}
	return vboxmanage.Host{Runner: vboxmanage.WinRMRunner{Target: e.Target, Auth: auth, Options: e.Options}, Path: e.VBoxManage}, nil
}

// VBox is the "vbox" sync plugin. The zero value is not usable; construct
// one with New.
type VBox struct {
	cfg     syncplugin.Config
	host    *vboxmanage.Host
	factory *inv.ItemFactory
	ruleSet *classification.RuleSet
	creds   credential.Store
}

// compile-time proof this plugin satisfies the port.
var _ syncplugin.Plugin = (*VBox)(nil)

// New creates an unconnected plugin from the dependencies the composition
// root supplies.
func New(deps syncplugin.Deps) *VBox {
	return &VBox{factory: inv.NewItemFactory(), ruleSet: classification.DefaultRuleSet(), creds: deps.Credentials}
}

// ReadEndpoint reads where the host is from cfg: Endpoint as an http or
// https URL (WinRM's 5985 or 5986 when it names no port), and the TLS and
// VBoxManage settings.
func ReadEndpoint(cfg syncplugin.Config) (Endpoint, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
		return Endpoint{}, fmt.Errorf("sync plugin %q: endpoint %q is not the host's WinRM address, as https://192.168.56.1:5986", Name, cfg.Endpoint)
	}
	e := Endpoint{Options: winrmexec.Options{Timeout: callTimeout, HTTPS: u.Scheme == "https", Insecure: cfg.InsecureSkipVerify}, VBoxManage: DefaultVBoxManage}
	e.Target.Host = u.Hostname()
	if p := u.Port(); p != "" {
		if e.Target.Port, err = strconv.Atoi(p); err != nil || e.Target.Port < 1 || e.Target.Port > 65535 {
			return Endpoint{}, fmt.Errorf("sync plugin %q: endpoint %q has no valid port", Name, cfg.Endpoint)
		}
	}
	e.Target.Port = winrmexec.ResolvePort(e.Target.Port, e.Options.HTTPS)
	if path, ok := cfg.Setting(SettingCAFile); ok && path != "" {
		if !e.Options.HTTPS {
			return Endpoint{}, fmt.Errorf("sync plugin %q: %s pins an authority for https, and the endpoint is http", Name, SettingCAFile)
		}
		// The operator names the file on the command line, and reads only
		// what they can already read; Clean drops nothing but redundancy.
		if e.Options.CACert, err = os.ReadFile(filepath.Clean(path)); err != nil {
			return Endpoint{}, fmt.Errorf("sync plugin %q: reading %s: %w", Name, SettingCAFile, err)
		}
	}
	if name, ok := cfg.Setting(SettingServerName); ok {
		e.Options.ServerName = name
	}
	if path, ok := cfg.Setting(SettingVBoxManage); ok && path != "" {
		if err := vboxmanage.CheckPath("VBoxManage", path); err != nil {
			return Endpoint{}, fmt.Errorf("sync plugin %q: %w", Name, err)
		}
		e.VBoxManage = path
	}
	return e, nil
}

// Connect resolves the credential, builds the host and lists its VMs once,
// so a wrong password or an unreachable host fails here rather than in
// the middle of a sync.
func (p *VBox) Connect(ctx context.Context, cfg syncplugin.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if p.creds == nil {
		return fmt.Errorf("sync plugin %q: no credential store configured", Name)
	}
	e, err := ReadEndpoint(cfg)
	if err != nil {
		return err
	}
	credentialName := cfg.CredentialName
	if credentialName == "" {
		credentialName = Name
	}
	cred, err := p.creds.Lookup(ctx, credentialName)
	if err != nil {
		return fmt.Errorf("sync plugin %q: resolving credential %q: %w", Name, credentialName, err)
	}
	h, err := HostFunc(e, credential.Flatten(cred))
	if err != nil {
		return fmt.Errorf("sync plugin %q: %w", Name, err)
	}
	if _, err := h.List(ctx); err != nil {
		return fmt.Errorf("sync plugin %q: listing the VMs on %s: %w", Name, cfg.Endpoint, err)
	}
	p.cfg, p.host = cfg, &h
	return nil
}

// Discover streams one record a VM, read as it is now.
func (p *VBox) Discover(ctx context.Context) (syncplugin.RecordIterator, error) {
	if p.host == nil {
		return nil, syncplugin.ErrNotConnected
	}
	refs, err := p.host.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync plugin %q: %w", Name, err)
	}
	return &vmIterator{host: *p.host, refs: refs, pos: -1}, nil
}

// Classify places a VM by its guest OS type, at the address recorded on
// it. Read-only, as the default is, it lands simulate-locked: nothing
// has logged in to the guest yet.
func (p *VBox) Classify(_ context.Context, rec record.Record) (syncplugin.Classification, error) {
	if reason, _ := rec.Properties[propUnreadable].(string); reason != "" {
		return syncplugin.Quarantine(reason), nil
	}
	if address, _ := rec.Properties[propHost].(string); address == "" {
		return syncplugin.Quarantine(fmt.Sprintf("VirtualBox records no address for %q: only a VM virt.vbox.vm.clone made carries one (the extradata %s), and VirtualBox cannot read a guest's address without its Guest Additions", rec.Name, vboxmanage.ExtraAddress)), nil
	}
	osType, _ := rec.Properties[propOSType].(string)
	path := classificationPath(osType)
	if path == nil {
		return syncplugin.Quarantine(fmt.Sprintf("no classification rule for the VirtualBox OS type %q yet", osType)), nil
	}
	state := inventory.StateSimulateLocked
	if !p.cfg.ReadOnly {
		state = inventory.StateActive
	}
	return syncplugin.ClassifyPath(p.ruleSet, path, state), nil
}

// classificationPath maps a VirtualBox OS type, as showvminfo describes it
// ("Windows Server 2025 (64-bit)", "Ubuntu (64-bit)"), onto a Section 6d
// path, or nil for one no rule places. FreeBSD is left out on purpose: its
// device is a generic one, whose capabilities onboarding probes.
func classificationPath(osType string) []string {
	lower := strings.ToLower(osType)
	switch {
	case strings.HasPrefix(lower, "windows"):
		return []string{"windows_server"}
	case strings.HasPrefix(lower, "ubuntu"), strings.HasPrefix(lower, "debian"):
		return []string{"linux_server", "debian_family"}
	case strings.HasPrefix(lower, "red hat"), strings.HasPrefix(lower, "fedora"), strings.HasPrefix(lower, "oracle linux"), strings.HasPrefix(lower, "oracle (64"):
		return []string{"linux_server", "rhel_family"}
	case strings.HasPrefix(lower, "linux"), strings.HasPrefix(lower, "other linux"):
		return []string{"linux_server"}
	}
	return nil
}

// Sync reconciles every VM into repo through the shared driver.
func (p *VBox) Sync(ctx context.Context, repo inv.Repository) (syncplugin.Reconciliation, error) {
	if p.host == nil {
		return syncplugin.Reconciliation{}, syncplugin.ErrNotConnected
	}
	return syncplugin.Reconcile(ctx, p, p.cfg, repo, p.factory)
}

// Close forgets the host. WinRM holds no connection between calls, so
// there is nothing else to release; it is safe to call twice.
func (p *VBox) Close() error {
	p.host = nil
	return nil
}
