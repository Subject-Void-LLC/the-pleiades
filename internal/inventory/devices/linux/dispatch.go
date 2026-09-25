// The properties a linux_server's constructor and accessors read, which
// are all of one that travels to a Runner.
package linux

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

// AccessorProperties returns the property keys a Server's constructor and
// accessors read. generic_ssh, which embeds Server for its accessors,
// declares the same set.
func AccessorProperties() []string {
	return []string{
		"apt_sources_list", "distribution", "dnf_repo_dir", FileTransferRootProperty, FirewalldProperty, "firewalld_zone",
		"host", "kernel_version", "passwd_path", "port", "service_manager", "shell", "systemd_unit_path", "working_directory",
	}
}

func init() { record.RegisterDispatchProperties("linux_server", AccessorProperties()...) }
