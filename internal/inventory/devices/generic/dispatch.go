// The properties each generic type's constructor and accessors read,
// which are all of one that travels to a Runner (with its discovery, which
// the Controller always adds).
package generic

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

func init() {
	// generic_ssh's accessors are linux.Server's.
	record.RegisterDispatchProperties(TypeSSH, linux.AccessorProperties()...)
	record.RegisterDispatchProperties(TypeNetconf, "host", "port", "netconf_port")
	record.RegisterDispatchProperties(TypeHTTP, append([]string{
		BaseURLProperty, HTTPAuthProperty, OpenAPIPathProperty, httpapi.AllowPlaintextCredentialsProperty,
	}, devicetls.Properties()...)...)
	record.RegisterDispatchProperties(TypeGRPC, append([]string{GRPCTargetProperty, GRPCPlaintextProperty}, devicetls.Properties()...)...)
}
