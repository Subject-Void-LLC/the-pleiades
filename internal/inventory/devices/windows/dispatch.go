// The properties a Windows server's accessors read, which are all of one
// that travels to a Runner.
package windows

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

func init() {
	// Every device TLS property, not only the two WinRM applies: the
	// constructor refuses the others, and a Runner rebuilding the device
	// must see them to refuse them too, rather than rebuild a device the
	// Controller would not load.
	record.RegisterDispatchProperties("windows_server", append([]string{
		"cmd_path", "dism_log_path", "host", "port", "powershell_path", "service_manager", "windows_edition",
		"windows_service_start_mode", "working_directory"}, devicetls.Properties()...)...)
}
