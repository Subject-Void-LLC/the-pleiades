// The properties an AWS account's accessors read, which are all of one
// that travels to a Runner.
package aws

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() { record.RegisterDispatchProperties("aws_account", "endpoint_override", "region") }
