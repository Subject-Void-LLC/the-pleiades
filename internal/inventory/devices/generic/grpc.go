// generic_grpc: a device that serves gRPC at one host:port.
package generic

import (
	"fmt"
	"net"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The properties generic_grpc reads.
const (
	GRPCTargetProperty    = "target"
	GRPCPlaintextProperty = "grpc_plaintext"
)

// GRPC is generic_grpc: a device that serves gRPC at one host:port.
// GRPCCapable is discovered: onboarding connects and asks the standard
// health and reflection services, and only an answer grants it.
type GRPC struct {
	*record.Base
	host string
}

// NewGRPC builds a generic_grpc device from rec, refusing a target that
// is not host:port with a port from 1 to 65535, and a grpc_plaintext that
// is not a boolean.
func NewGRPC(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	target, _ := props.String(GRPCTargetProperty)
	host, err := ValidateGRPCTarget(target)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGRPC, rec.Name, err)
	}
	if raw, present := rec.Properties[GRPCPlaintextProperty]; present {
		if _, ok := raw.(bool); !ok {
			return nil, fmt.Errorf("%s %s: property %s must be true or false", TypeGRPC, rec.Name, GRPCPlaintextProperty)
		}
	}
	caps, err := declared(TypeGRPC, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &GRPC{Base: record.NewBase(rec, caps), host: host}, nil
}

// ValidateGRPCTarget checks target is host:port and returns the host. A
// gRPC name-resolver scheme (dns:///, unix:) is refused: the target is an
// address, and a scheme would let an inventory value choose how, and
// where, the platform connects.
func ValidateGRPCTarget(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("property %s is required", GRPCTargetProperty)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return "", fmt.Errorf("property %s must be host:port", GRPCTargetProperty)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("property %s has no valid port", GRPCTargetProperty)
	}
	for _, r := range host {
		if r <= ' ' || r == '/' || r == 0x7f {
			return "", fmt.Errorf("property %s has a host that is not a name or address", GRPCTargetProperty)
		}
	}
	return host, nil
}

// HasCapability checks the declared set AND the structural assertion.
func (g *GRPC) HasCapability(name capability.Name) bool {
	return g.Declares(name) && capability.Implements(g, name)
}

// GRPCTarget returns the validated host:port.
func (g *GRPC) GRPCTarget() string {
	target, _ := g.Properties().String(GRPCTargetProperty)
	return target
}

// GRPCPlaintext reports whether grpc_plaintext is true; it is false when
// unset.
func (g *GRPC) GRPCPlaintext() bool {
	plain, _ := g.Properties().Bool(GRPCPlaintextProperty)
	return plain
}

// IPAddress returns the target's host.
func (g *GRPC) IPAddress() string {
	return g.host
}
