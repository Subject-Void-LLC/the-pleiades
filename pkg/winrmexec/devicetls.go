// A device's own TLS settings, applied to a WinRM connection.
package winrmexec

import "github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"

// WithDeviceTLS returns opts carrying the authority and server name a
// device's record pins (tls_ca_pem, tls_server_name), so a host whose
// listener certificate comes from a private authority is verified against
// that authority rather than the system's roots. Settings that name
// neither leave opts as they were.
//
// Only these two apply to WinRM. A device type reached over WinRM refuses
// the others when its record is loaded (windows_server does), because a
// version floor or a cipher allowance a connection silently ignored would
// be a setting that only appears to work.
func WithDeviceTLS(opts Options, settings devicetls.Settings) Options {
	if pem := settings.CAPEM(); len(pem) > 0 {
		opts.CACert = pem
	}
	if settings.ServerName != "" {
		opts.ServerName = settings.ServerName
	}
	return opts
}
