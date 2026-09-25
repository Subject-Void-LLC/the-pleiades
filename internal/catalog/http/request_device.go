// http.request's device mode: a url that is a path is a call to the
// target device's own API, carrying that device's credential and no other.
package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// requestDevice is a device-mode request's target API: its base URL, and
// how the device's own stored credential is sent to it.
//
// Device mode is chosen by the url parameter alone. A path beginning with
// one slash is a path on the target device's API, and only such a request
// carries a credential, the device's own, taken from the credential store
// and never from the runbook. A full URL is a call to whatever it names,
// as before, and carries nothing. So a credential reaches exactly the
// origin the device's inventory record names: pkg/httpapi joins the path
// so it cannot leave that origin, redirects are followed only within it,
// and the task may set neither Authorization nor Host.
type requestDevice struct {
	base *url.URL
	auth string
}

// requestIsDevicePath reports whether raw is a path on the target
// device's API rather than a URL.
func requestIsDevicePath(raw string) bool {
	return strings.HasPrefix(raw, "/")
}

// requestDeviceURL resolves raw against device's API, refusing a device
// that has not proved it serves one (an onboarded generic_http device
// declares HTTPAPICapable).
func requestDeviceURL(device inventory.InventoryItem, raw string) (string, *requestDevice, error) {
	if device == nil {
		return "", nil, fmt.Errorf("%s %q is a path on a device's API, and this task has no target device", requestParamURL, raw)
	}
	if !device.HasCapability(capability.NameHTTPAPI) {
		return "", nil, fmt.Errorf("%s %q is a path on a device's API, and device %q does not declare %s (onboard a generic_http device first)",
			requestParamURL, raw, device.Name(), capability.NameHTTPAPI)
	}
	// Declared but with no base URL to read is the Walk tier today: a
	// Runner rebuilds a dispatched device from its SSH address and
	// capability names only, so the accessor is not there to call.
	api, ok := device.(capability.HTTPAPICapable)
	if !ok {
		return "", nil, fmt.Errorf("%s %q is a path on a device's API, and device %q's base URL is not available where this task runs (a Runner receives only a device's SSH address)",
			requestParamURL, raw, device.Name())
	}
	base, err := httpapi.ValidateBaseURL(api.HTTPBaseURL(), api.HTTPAuth())
	if err != nil {
		return "", nil, fmt.Errorf("device %q: %w", device.Name(), err)
	}
	full, err := httpapi.Join(base, raw)
	if err != nil {
		return "", nil, fmt.Errorf("%s %q: %w", requestParamURL, raw, err)
	}
	return full.String(), &requestDevice{base: base, auth: api.HTTPAuth()}, nil
}

// check refuses what a device-mode request may not carry: an
// Authorization or Host header, since the credential and the origin are
// the device's, and turning certificate checks off for a request that
// sends a credential.
func (d *requestDevice) check(headers map[string]string, validateCerts bool) error {
	for name := range headers {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Host") {
			return fmt.Errorf("%s may not set %s on a path on a device's API: the device's own record decides both", requestParamHeaders, name)
		}
	}
	if !validateCerts && d.auth != httpapi.AuthNone {
		return fmt.Errorf("%s false is refused on a path on a device's API that sends the device's credential", requestParamValidateCerts)
	}
	return nil
}

// checkRedirect follows a redirect only within the device's origin, and
// otherwise stops, returning the redirect itself as the response for the
// status check to judge.
func (d *requestDevice) checkRedirect(req *nethttp.Request, via []*nethttp.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !httpapi.SameOrigin(d.base, req.URL) {
		return nethttp.ErrUseLastResponse
	}
	return nil
}
