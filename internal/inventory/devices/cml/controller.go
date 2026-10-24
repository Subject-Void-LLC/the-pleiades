// Package cml holds the cml_controller device type: a Cisco Modeling Labs 2
// controller, the API account the virt.cml methods build labs through and
// import-cml --controller reads, and the console server a node with no
// address of its own is reached through.
package cml

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TypeController is the cml_controller device type.
const TypeController = "cml_controller"

// The properties a controller's record holds beside its TLS settings.
const (
	// APIBaseProperty is the controller's base URL, e.g.
	// https://10.10.20.161: scheme, host and port only.
	APIBaseProperty = "api_base"
	// ConsolePortProperty is the port of the controller's console server,
	// an SSH service on the controller's host (22 when unset).
	ConsolePortProperty = "console_port"
)

// defaultConsolePort is where CML's console server listens.
const defaultConsolePort = 22

// Controller is a cml_controller device.
type Controller struct {
	*record.Base
	base        string
	host        string
	plaintext   bool
	consolePort int
	tls         devicetls.Settings
}

// NewController builds a cml_controller from rec, refusing a base URL that
// is not http:// or https:// with a host and nothing after it, a plain
// http:// one unless the record allows sending the login in the clear
// (httpapi's rule and flag), TLS settings on a plain http:// base, and a
// console port outside 1-65535.
func NewController(rec record.Record) (inventory.InventoryItem, error) {
	fail := func(err error) (inventory.InventoryItem, error) {
		return nil, fmt.Errorf("%s %s: %w", TypeController, rec.Name, err)
	}
	props := inventory.NewProperties(rec.Properties)
	raw, _ := props.String(APIBaseProperty)
	allow, present := rec.Properties[httpapi.AllowPlaintextCredentialsProperty]
	plaintext, isBool := allow.(bool)
	if present && !isBool {
		return fail(fmt.Errorf("property %s must be true or false", httpapi.AllowPlaintextCredentialsProperty))
	}
	base, err := httpapi.ValidateBaseURL(raw, httpapi.AuthBasic, plaintext)
	if err != nil {
		return fail(fmt.Errorf("property %s: %w", APIBaseProperty, err))
	}
	if base.Path != "" && base.Path != "/" {
		return fail(fmt.Errorf("property %s is the controller's base URL, scheme, host and port only, without /api/v0", APIBaseProperty))
	}
	if base.Scheme == "http" {
		for _, key := range devicetls.Properties() {
			if _, present := rec.Properties[key]; present {
				return fail(fmt.Errorf("property %s applies to a TLS connection, and this controller's has none", key))
			}
		}
	}
	settings, err := devicetls.Parse(props)
	if err != nil {
		return fail(err)
	}
	port := defaultConsolePort
	if v, present := rec.Properties[ConsolePortProperty]; present {
		n, ok := portNumber(v)
		if !ok {
			return fail(fmt.Errorf("property %s must be a port number, 1 to 65535", ConsolePortProperty))
		}
		port = n
	}
	caps, _, err := record.Declared(TypeController, rec, []capability.Name{capability.NameNetworkAddressable, capability.NameCMLAPI})
	if err != nil {
		return nil, err
	}
	base.Path = ""
	return &Controller{Base: record.NewBase(rec, caps), base: base.String(), host: base.Hostname(), plaintext: plaintext, consolePort: port, tls: settings}, nil
}

// portNumber reads a port YAML or --set may hand over as a whole number.
func portNumber(v inventory.PropertyValue) (int, bool) {
	var n int
	switch p := v.(type) {
	case int:
		n = p
	case float64:
		if p != float64(int(p)) {
			return 0, false
		}
		n = int(p)
	default:
		return 0, false
	}
	return n, n >= 1 && n <= 65535
}

// HasCapability checks the declared set AND the structural assertion.
func (c *Controller) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// CMLBaseURL implements capability.CMLAPICapable.
func (c *Controller) CMLBaseURL() string { return c.base }

// CMLPlaintextWarning implements capability.CMLAPICapable.
func (c *Controller) CMLPlaintextWarning() string {
	if !c.plaintext {
		return ""
	}
	return fmt.Sprintf("%s is logged in to over plain HTTP at %s (%s): its username and password, and the bearer token the controller answers with, cross the network unencrypted",
		c.Name(), c.host, httpapi.AllowPlaintextCredentialsProperty)
}

// CMLConsoleAddress implements capability.CMLAPICapable: the console
// server listens on the controller's own host.
func (c *Controller) CMLConsoleAddress() (string, int) { return c.host, c.consolePort }

// TLSSettings returns the controller's validated TLS settings.
func (c *Controller) TLSSettings() devicetls.Settings { return c.tls }

// IPAddress returns the base URL's host.
func (c *Controller) IPAddress() string { return c.host }

// SharedAcrossRuns implements inventory.Shared: runs build and read labs
// of their own on one controller at once, so no lease serializes them.
func (c *Controller) SharedAcrossRuns() bool { return true }

func init() {
	record.RegisterType(TypeController, NewController)
	// The controller logs in with its own account, and a template's
	// machine credential is never sent to it.
	record.RegisterOwnCredentialType(TypeController)
	record.RegisterDispatchProperties(TypeController,
		append([]string{APIBaseProperty, ConsolePortProperty, httpapi.AllowPlaintextCredentialsProperty}, devicetls.Properties()...)...)
}
