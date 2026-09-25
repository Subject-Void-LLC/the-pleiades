// The properties a Windows server's accessors read, which are all of one
// that travels to a Runner.
package windows

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() {
	record.RegisterDispatchProperties("windows_server",
		"cmd_path", "dism_log_path", "host", "port", "powershell_path", "service_manager", "windows_edition",
		"windows_service_start_mode", "working_directory")
}
