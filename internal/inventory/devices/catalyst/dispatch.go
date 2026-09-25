// The properties a Catalyst Center's accessors read, which are all of one
// that travels to a Runner.
package catalyst

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() { record.RegisterDispatchProperties("catalyst_center", "catalyst_base_url", "host") }
