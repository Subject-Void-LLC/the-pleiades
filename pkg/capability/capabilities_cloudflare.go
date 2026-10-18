// The capability of a Cloudflare account: an API this platform reads with
// the account's own token (pkg/cfapi).
package capability

// NameCloudflareAPI names CloudflareAPICapable.
const NameCloudflareAPI Name = "CloudflareAPICapable"

// CloudflareAPICapable is satisfied by a device that is a Cloudflare
// account reached through Cloudflare's API.
type CloudflareAPICapable interface {
	// CloudflareAccountID is the account's id, 32 hex digits.
	CloudflareAccountID() string
	// CloudflareAPIBase is the API's base URL, https.
	CloudflareAPIBase() string
}

func init() {
	Register(Descriptor{
		Name:   NameCloudflareAPI,
		Assert: func(item any) bool { _, ok := item.(CloudflareAPICapable); return ok },
	})
}
