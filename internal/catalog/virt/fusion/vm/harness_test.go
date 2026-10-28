// The harness the vm methods' tests share: a model Fusion host in place of a
// real Mac, whose answers and failure messages are the ones a real Fusion
// 26.0.1 gave (pkg/vmrun/testdata), and a context that keeps what a method
// records. Each test calls a method through its registered descriptor, so
// what it exercises is what the dispatcher calls.
package vm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmrun"
)

const vmx = "/Users/me/pleiades-vms/win11-arm/win11-arm.vmx"

// recorder is a RunbookContext keeping each stat a method sets.
type recorder struct{ stats map[string]any }

func newRecorder() *recorder                            { return &recorder{stats: map[string]any{}} }
func (r *recorder) InjectSecrets() map[string]string    { return nil }
func (r *recorder) SetStat(key string, value any) error { r.stats[key] = value; return nil }
func (r *recorder) EmitFact(key string, value any) error {
	return r.SetStat(key, value)
}

// inverse is the undo a method recorded, or nil.
func (r *recorder) inverse() map[string]any {
	inv, _ := r.stats[sdk.StatInverse].(map[string]any)
	return inv
}

// model is a Mac running Fusion with one VM at vmx.
type model struct {
	exists bool
	state  string
	tools  bool
	// resumed is a guest just resumed from suspend: a plain getGuestIPAddress
	// says the tools are not running although they are, as a real Fusion
	// 26.0.1 answered, and only -wait gives the address.
	resumed bool
	ip      string
	pid     int
	calls   []string
	// config is the VM's vmx keys as vmcli's ConfigParams query prints
	// them, and snapshots its snapshot names; a nil config fails the query.
	config    string
	snapshots []string
}

// Run answers each command a method sends, as Fusion would.
func (m *model) Run(_ context.Context, argv []string) (vmrun.Output, error) {
	m.calls = append(m.calls, strings.Join(argv, " "))
	fail := func(msg string) (vmrun.Output, error) {
		return vmrun.Output{Stdout: "Error: " + msg + "\n", ExitCode: 255}, nil
	}
	last := argv[len(argv)-1]
	switch {
	case argv[0] == "/bin/test":
		if m.exists {
			return vmrun.Output{}, nil
		}
		return vmrun.Output{ExitCode: 1}, nil
	case argv[0] == vmrun.FusionVMCLI && len(argv) == 4 && argv[2] == "Power" && argv[3] == "query":
		return vmrun.Output{Stdout: "PowerState: " + m.state + "\ncleanShutdown: true\n"}, nil
	case argv[0] == "/bin/sh" && strings.Contains(strings.Join(argv, " "), " start "+vmx+" nogui"):
		m.state = stateOn
		return vmrun.Output{Stdout: "LocationGetRoot: Unable to determine the product location.\n"}, nil
	case argv[0] == "/bin/sh" && last == vmx && strings.Contains(argv[2], "caffeinate"):
		if m.state != stateOn {
			return vmrun.Output{ExitCode: 3}, nil
		}
		return vmrun.Output{Stdout: fmt.Sprintf("%d\n", m.pid)}, nil
	case argv[0] == vmrun.FusionVMRun && len(argv) >= 5 && (argv[3] == "stop" || argv[3] == "suspend"):
		if m.state != stateOn {
			return fail("The virtual machine is not powered on: " + vmx)
		}
		if argv[3] == "stop" && last == "soft" && !m.tools {
			return fail("The VMware Tools are not running in the virtual machine: " + vmx)
		}
		m.state = stateOff
		if argv[3] == "suspend" {
			m.state = stateSuspended
		}
		return vmrun.Output{}, nil
	case argv[0] == vmrun.FusionVMCLI && len(argv) == 4 && argv[2] == "ConfigParams" && argv[3] == "query":
		if m.config == "" {
			return vmrun.Output{Stderr: "vmcli: cannot read the configuration\n", ExitCode: 1}, nil
		}
		return vmrun.Output{Stdout: m.config}, nil
	case argv[0] == vmrun.FusionVMRun && argv[3] == "listSnapshots":
		return vmrun.Output{Stdout: fmt.Sprintf("Total snapshots: %d\n%s", len(m.snapshots), strings.Join(append(m.snapshots, ""), "\n"))}, nil
	case argv[0] == vmrun.FusionVMRun && argv[3] == "checkToolsState":
		if m.tools {
			return vmrun.Output{Stdout: "running\n"}, nil
		}
		return vmrun.Output{Stdout: "installed\n"}, nil
	case argv[0] == vmrun.FusionVMRun && argv[3] == "getGuestIPAddress":
		switch {
		case m.state != stateOn:
			return fail("The virtual machine is not powered on: " + vmx)
		case !m.tools, m.resumed && last != "-wait":
			return fail("The VMware Tools are not running in the virtual machine: " + vmx)
		}
		return vmrun.Output{Stdout: m.ip + "\n"}, nil
	}
	return vmrun.Output{}, fmt.Errorf("model: no answer for %q", argv)
}

// changes is every call other than a read, which a check must never make.
func (m *model) changes() []string {
	var out []string
	for _, c := range m.calls {
		if strings.HasPrefix(c, "/bin/test ") || strings.HasSuffix(c, " Power query") || strings.Contains(c, " getGuestIPAddress ") || strings.Contains(c, " checkToolsState ") ||
			strings.HasSuffix(c, " ConfigParams query") || strings.Contains(c, " listSnapshots ") {
			continue
		}
		out = append(out, c)
	}
	return out
}

// mac is the Fusion host every test targets.
var mac = &inventorytest.Stub{StubName: "mac", Caps: []capability.Name{capability.NameVMwareFusion, capability.NameSSHTransport}}

// onModel makes each method reach m instead of a real Mac, and records that
// the connection was closed.
func onModel(t *testing.T, m *model) *bool {
	t.Helper()
	old := hostFunc
	closed := new(bool)
	hostFunc = func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (vmrun.Host, func(), error) {
		return vmrun.Host{Runner: m, Product: vmrun.Fusion, VMRun: vmrun.FusionVMRun, VMCLI: vmrun.FusionVMCLI}, func() { *closed = true }, nil
	}
	t.Cleanup(func() { hostFunc = old })
	return closed
}

// call runs fqcn's Invoke, or its Check when check is true, through the
// registered descriptor.
func call(t *testing.T, fqcn string, check bool, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, mac, params)
	}
	return d.Invoke(context.Background(), rc, mac, params)
}
