// The properties a container host's accessors read, which are all of one
// that travels to a Runner. Its discovery travels with them, as every
// type's does.
package container

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

func init() {
	record.RegisterDispatchProperties(TypeHost, append([]string{"host", "port", EngineTLSProperty}, devicetls.Properties()...)...)
}
