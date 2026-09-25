// The TLS settings a Windows server's WinRM connection applies.
package windows

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// winrmTLS reads the device TLS properties WinRM applies: a pinned
// authority (tls_ca_pem) for a host whose listener certificate comes from
// a private one, and the name that certificate is checked against
// (tls_server_name). Every other device TLS property is refused rather
// than accepted and ignored: WinRM's certificate path is capped at TLS 1.2
// and takes its client certificate from the credential, so a version
// floor, a cipher allowance or tls_client_certificate here would be a
// setting that only appears to work.
//
// The two it applies are refused on port 5985 for the same reason: that
// is the HTTP listener, which has no TLS to verify, so a pin there would
// be ignored on every run.
func winrmTLS(rec record.Record) (devicetls.Settings, error) {
	for _, key := range devicetls.Properties() {
		if key == devicetls.CAPEMProperty || key == devicetls.ServerNameProperty {
			continue
		}
		if _, set := rec.Properties[key]; set {
			return devicetls.Settings{}, fmt.Errorf("property %s does not apply to WinRM; a windows_server applies only %s and %s",
				key, devicetls.CAPEMProperty, devicetls.ServerNameProperty)
		}
	}
	props := inventory.NewProperties(rec.Properties)
	settings, err := devicetls.Parse(props)
	if err != nil {
		return devicetls.Settings{}, err
	}
	if (settings.PinnedCA() || settings.ServerName != "") && winrmPort(props) == winrmexec.DefaultPort {
		return devicetls.Settings{}, fmt.Errorf("%s and %s apply to the HTTPS listener, and port %d is the HTTP one, which has no TLS: set port to %d, or remove them",
			devicetls.CAPEMProperty, devicetls.ServerNameProperty, winrmexec.DefaultPort, winrmexec.DefaultPortHTTPS)
	}
	return settings, nil
}

// TLSSettings implements devicetls.Configured: the authority and server
// name this server's WinRM listener is verified with.
func (w *Server) TLSSettings() devicetls.Settings { return w.tls }
