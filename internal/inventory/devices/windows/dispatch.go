// The properties a Windows server's accessors read, which are all of one
// that travels to a Runner.
package windows

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() {
	record.RegisterDispatchProperties("windows_server",
		"dism_log_path", "host", "port", "service_manager", "windows_edition", "windows_service_start_mode")
}
