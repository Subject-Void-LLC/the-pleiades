// The properties a container host's accessors read, which are all of one
// that travels to a Runner.
package container

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() { record.RegisterDispatchProperties("container_host", "docker_endpoint", "host", "port") }
