package linux_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestNewServer_BaselineCapabilities is a regression proof: a Record with
// no classification-derived Capabilities (the explicit-Type hydration
// path, unchanged since before Phase 32) still gets exactly the vendor
// baseline it always did.
func TestNewServer_BaselineCapabilities(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("expected the vendor baseline to include SSHTransportCapable")
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Error("expected the vendor baseline to include LinuxCapable")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected no capability beyond the vendor baseline with no classification data")
	}

	// ShellExecCapable is the narrower of the two exec capabilities, and
	// declaring it must satisfy a method requiring the broader
	// CommandExecCapable through capability.Resolves. exec.command
	// requires exactly that, so this is what makes a Linux server a legal
	// target for it.
	if !item.HasCapability(capability.NameShellExec) {
		t.Error("expected the vendor baseline to include ShellExecCapable")
	}
	if !item.HasCapability(capability.NameCommandExec) {
		t.Error("expected declaring ShellExecCapable to also satisfy CommandExecCapable, which exec.command requires")
	}
}

// TestNewServer_UnionsClassificationCapabilities mirrors
// cisco.TestNewRouter_UnionsClassificationCapabilities: classification-
// derived Capabilities are unioned into the declared set at the data
// layer, but HasCapability(NameApt) correctly stays false since Server
// does not structurally implement AptCapable's methods -- "neither side
// is trusted alone."
func TestNewServer_UnionsClassificationCapabilities(t *testing.T) {
	rec := record.Record{
		ID:           "s1",
		Name:         "s1",
		Type:         "linux_server",
		Capabilities: []capability.Name{capability.NameApt},
	}
	item, err := linux.NewServer(rec)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) || !item.HasCapability(capability.NameLinux) {
		t.Error("expected the vendor baseline to survive alongside classification-derived capabilities")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected HasCapability(AptCapable) to stay false: Server does not structurally implement it")
	}

	found := false
	for _, c := range item.Capabilities() {
		if c == capability.NameApt {
			found = true
		}
	}
	if !found {
		t.Error("expected the classification-derived AptCapable to be unioned into the declared set")
	}
}

// TestServer_Accessors exercises every structural accessor method Server
// implements to back its capability interfaces, reading through
// Properties() exactly as the real SSH transport code does.
func TestServer_Accessors(t *testing.T) {
	rec := record.Record{
		ID:   "s1",
		Name: "s1",
		Type: "linux_server",
		Properties: map[string]inventory.PropertyValue{
			"host":              "10.0.0.2",
			"port":              2222,
			"kernel_version":    "6.6.1",
			"distribution":      "ubuntu",
			"working_directory": "/srv/app",
			"shell":             "/bin/bash",
		},
	}
	item, err := linux.NewServer(rec)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server, ok := item.(*linux.Server)
	if !ok {
		t.Fatalf("NewServer returned %T, want *linux.Server", item)
	}

	if got := server.SSHHost(); got != "10.0.0.2" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.2")
	}
	if got := server.IPAddress(); got != "10.0.0.2" {
		t.Errorf("IPAddress() = %q, want %q", got, "10.0.0.2")
	}
	if got := server.SSHPort(); got != 2222 {
		t.Errorf("SSHPort() = %d, want %d", got, 2222)
	}
	if got := server.KernelVersion(); got != "6.6.1" {
		t.Errorf("KernelVersion() = %q, want %q", got, "6.6.1")
	}
	if got := server.Distribution(); got != "ubuntu" {
		t.Errorf("Distribution() = %q, want %q", got, "ubuntu")
	}
	if got := server.WorkingDirectory(); got != "/srv/app" {
		t.Errorf("WorkingDirectory() = %q, want %q", got, "/srv/app")
	}
	if got := server.ShellPath(); got != "/bin/bash" {
		t.Errorf("ShellPath() = %q, want %q", got, "/bin/bash")
	}
}

// TestServer_ExecAccessorDefaults proves the two accessors added for the
// exec capabilities behave sensibly on a device whose inventory entry
// says nothing about either.
//
// The two defaults differ on purpose. /bin/sh is the POSIX-guaranteed
// path every distribution ships, so it is a real answer. There is no
// equivalent real answer for a working directory: a hardcoded /root or
// /home/<user> would be wrong for most accounts and would silently move
// where a relative path resolves, so an empty string, meaning "wherever
// this account lands on login," is the only correct default.
func TestServer_ExecAccessorDefaults(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server := item.(*linux.Server)

	if got := server.WorkingDirectory(); got != "" {
		t.Errorf("WorkingDirectory() = %q, want an empty string when the inventory entry sets none", got)
	}
	if got := server.ShellPath(); got != "/bin/sh" {
		t.Errorf("ShellPath() = %q, want the POSIX default %q", got, "/bin/sh")
	}
}

// TestServer_SSHPort_DefaultsTo22 proves the fallback branch when no port
// property is set.
func TestServer_SSHPort_DefaultsTo22(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server := item.(*linux.Server)
	if got := server.SSHPort(); got != 22 {
		t.Errorf("SSHPort() = %d, want default 22", got)
	}
}

// TestServer_SatisfiesEveryImplementedMethodsCapability is the test that
// would have caught this whole class of problem before enforcement
// existed.
//
// Every implemented Collection method names its RequiredCapabilities, and
// until engine.checkMethodCapabilities landed nothing compared that list
// against a device. linux_server declared SSHTransportCapable,
// LinuxCapable and ShellExecCapable only, while the file.* methods
// required POSIXFileSystemCapable and facts.gather required
// FactGathererCapable. Fifteen methods had been running against real
// Linux hosts on a claim their target device never actually made.
//
// It checks HasCapability rather than the declared list, so a capability
// declared without its accessor methods still fails: HasCapability is
// Declares AND capability.Implements, and a declaration the Go type
// cannot back is exactly the failure this guards.
func TestServer_SatisfiesEveryImplementedMethodsCapability(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "l1", Name: "l1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// One entry per capability an implemented method requires today,
	// with the method that requires it, so a failure names the runbook
	// task that would break rather than only the capability.
	required := []struct {
		capability capability.Name
		requiredBy string
	}{
		{capability.NameSSHTransport, "net.ssh.ping"},
		{capability.NameCommandExec, "exec.command"},
		{capability.NameShellExec, "exec.shell"},
		{capability.NamePOSIXFileSystem, "file.*, wait.path, wait.search"},
		{capability.NameFactGatherer, "facts.gather"},
		{capability.NameSystemd, "svc.systemd.*"},
		{capability.NameServiceManager, "svc.* (resolved up from SystemdCapable)"},
	}

	for _, r := range required {
		t.Run(string(r.capability), func(t *testing.T) {
			if !item.HasCapability(r.capability) {
				t.Errorf("linux_server does not have %s, so %s can no longer run against it", r.capability, r.requiredBy)
			}
		})
	}
}

// TestServer_ServiceManagerAccessors covers the defaults and the escape
// hatch.
//
// The default matters because SystemdCapable is declared in the baseline
// for every linux_server, which is a claim about the mainstream case
// rather than about every Linux system. The property is how an OpenRC or
// Alpine host says otherwise, letting a method refuse it by name instead
// of running systemctl and reporting whatever "command not found" looks
// like on that host.
func TestServer_ServiceManagerAccessors(t *testing.T) {
	tests := []struct {
		name           string
		props          map[string]inventory.PropertyValue
		wantManager    string
		wantUnitPath   string
		wantRootPath   string
		wantFactSource string
	}{
		{
			name:           "defaults",
			props:          nil,
			wantManager:    "systemd",
			wantUnitPath:   "/etc/systemd/system",
			wantRootPath:   "/",
			wantFactSource: "setup",
		},
		{
			name:           "a host that does not run systemd says so",
			props:          map[string]inventory.PropertyValue{"service_manager": "openrc"},
			wantManager:    "openrc",
			wantUnitPath:   "/etc/systemd/system",
			wantRootPath:   "/",
			wantFactSource: "setup",
		},
		{
			name:           "a relocated unit directory",
			props:          map[string]inventory.PropertyValue{"systemd_unit_path": "/usr/local/lib/systemd/system"},
			wantManager:    "systemd",
			wantUnitPath:   "/usr/local/lib/systemd/system",
			wantRootPath:   "/",
			wantFactSource: "setup",
		},
		{
			name:           "empty strings fall back rather than being honored",
			props:          map[string]inventory.PropertyValue{"service_manager": "", "systemd_unit_path": ""},
			wantManager:    "systemd",
			wantUnitPath:   "/etc/systemd/system",
			wantRootPath:   "/",
			wantFactSource: "setup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := linux.NewServer(record.Record{
				ID: "l1", Name: "l1", Type: "linux_server", Properties: tt.props,
			})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			dev, ok := item.(capability.SystemdCapable)
			if !ok {
				t.Fatal("linux.Server does not satisfy capability.SystemdCapable")
			}
			if got := dev.ServiceManagerName(); got != tt.wantManager {
				t.Errorf("ServiceManagerName() = %q, want %q", got, tt.wantManager)
			}
			if got := dev.SystemdUnitPath(); got != tt.wantUnitPath {
				t.Errorf("SystemdUnitPath() = %q, want %q", got, tt.wantUnitPath)
			}
			posix, ok := item.(capability.POSIXFileSystemCapable)
			if !ok {
				t.Fatal("linux.Server does not satisfy capability.POSIXFileSystemCapable")
			}
			if got := posix.RootPath(); got != tt.wantRootPath {
				t.Errorf("RootPath() = %q, want %q", got, tt.wantRootPath)
			}
			facts, ok := item.(capability.FactGathererCapable)
			if !ok {
				t.Fatal("linux.Server does not satisfy capability.FactGathererCapable")
			}
			if got := facts.FactSourceName(); got != tt.wantFactSource {
				t.Errorf("FactSourceName() = %q, want %q", got, tt.wantFactSource)
			}
		})
	}
}
