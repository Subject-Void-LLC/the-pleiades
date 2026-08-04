package capability_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

type sshDevice struct{}

func (sshDevice) SSHHost() string { return "10.0.0.1" }
func (sshDevice) SSHPort() int    { return 22 }

type nonSSHDevice struct{}

type iosDevice struct{}

func (iosDevice) IOSVersion() string    { return "17.3.2" }
func (iosDevice) SupportsNETCONF() bool { return true }

type linuxDevice struct{}

func (linuxDevice) KernelVersion() string { return "6.6.1" }
func (linuxDevice) Distribution() string  { return "ubuntu" }

// TestBlessedCapabilitiesRegistered enforces the binding rule: every
// blessed capability name must resolve to a Descriptor whose Assert
// function actually distinguishes an implementing type from one that does
// not implement it. This is what makes HasCapability returning true a
// guarantee instead of an assertion left to a comment.
func TestBlessedCapabilitiesRegistered(t *testing.T) {
	names := []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS, capability.NameLinux}
	for _, name := range names {
		if _, ok := capability.Lookup(name); !ok {
			t.Errorf("capability %q is not registered", name)
		}
	}
}

func TestImplementsSSHTransport(t *testing.T) {
	if !capability.Implements(sshDevice{}, capability.NameSSHTransport) {
		t.Error("expected sshDevice to implement SSHTransportCapable")
	}
	if capability.Implements(nonSSHDevice{}, capability.NameSSHTransport) {
		t.Error("expected nonSSHDevice to NOT implement SSHTransportCapable")
	}
}

func TestImplementsCiscoIOS(t *testing.T) {
	if !capability.Implements(iosDevice{}, capability.NameCiscoIOS) {
		t.Error("expected iosDevice to implement CiscoIOSCapable")
	}
	if capability.Implements(nonSSHDevice{}, capability.NameCiscoIOS) {
		t.Error("expected nonSSHDevice to NOT implement CiscoIOSCapable")
	}
}

func TestImplementsLinux(t *testing.T) {
	if !capability.Implements(linuxDevice{}, capability.NameLinux) {
		t.Error("expected linuxDevice to implement LinuxCapable")
	}
	if capability.Implements(nonSSHDevice{}, capability.NameLinux) {
		t.Error("expected nonSSHDevice to NOT implement LinuxCapable")
	}
}

func TestImplementsUnknownCapability(t *testing.T) {
	if capability.Implements(sshDevice{}, capability.Name("NotARealCapability")) {
		t.Error("expected an unregistered capability name to never be implemented")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected Register to panic on a duplicate capability name")
		}
	}()
	capability.Register(capability.Descriptor{
		Name:   capability.NameSSHTransport,
		Assert: func(item any) bool { return true },
	})
}
