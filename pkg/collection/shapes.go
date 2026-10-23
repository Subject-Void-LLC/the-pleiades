// A method whose calls take more than one shape: what each shape needs of
// its device, chosen by a parameter the call gives.
//
// Most methods reach their device one way. A few reach it two: net.trace
// traces from a Linux device over SSH, or, given container, from a
// container on a container host through the host's engine. The two need
// different capabilities and different transports, and a manifest's
// RequiredCapabilities are all required at once, so neither one list nor
// their union can say it. The generic svc.* and pkg.* dispatchers solve a
// near cousin of this by declaring a parent capability and the union of
// transports, then holding the concrete method to its own; that works
// because the device decides which concrete method runs. Here the call
// decides, from its parameters, and so the shape is data a call selects
// (Manifest.Shapes), held at plan time (validate's capability and
// transport rules), at run time (the engine) and at a Controller's
// admission alike, through the functions below.
package collection

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// CallShape is one more shape a method's call can take, beside the one
// its manifest's own transports and capabilities describe. A call that
// gives Param (present and not null) is held to this shape's transports
// and capabilities in place of the manifest's.
type CallShape struct {
	// Param is the parameter whose presence selects this shape. It is one
	// of the method's documented parameters.
	Param string `json:"param"`
	// SupportedTransports are this shape's, as Manifest's are.
	SupportedTransports []string `json:"supportedTransports,omitempty"`
	// RequiredCapabilities are this shape's, as Manifest's are.
	RequiredCapabilities []capability.Name `json:"requiredCapabilities,omitempty"`
}

// For returns the manifest a call with params is held to: m itself, or, when
// params give the Param of one of m.Shapes, m with that shape's transports
// and capabilities in place of its own (and no shapes). The first shape
// given wins; a method whose shapes cannot combine refuses a call giving
// two itself.
//
// A parameter is given when it is present and not null, whatever its value:
// an empty string selects the shape too, since a template rendering empty
// is not known to be empty when validation reads it. A method selecting by
// a text parameter must therefore refuse an empty one itself (net.trace's
// container does), never fall back to its manifest's own shape, which the
// call was not held to.
func (m Manifest) For(params map[string]any) Manifest {
	for _, s := range m.Shapes {
		if v, ok := params[s.Param]; ok && v != nil {
			m.SupportedTransports = s.SupportedTransports
			m.RequiredCapabilities = s.RequiredCapabilities
			m.Shapes = nil
			return m
		}
	}
	return m
}

// CheckShape refuses a device a call held to m cannot run on: a capability
// it lacks (CheckCapabilities), then a transport it does not reach
// (CheckTransports). m is a manifest For has already shaped.
func CheckShape(device inventory.InventoryItem, fqcn string, m Manifest) error {
	if err := CheckCapabilities(device, fqcn, m); err != nil {
		return err
	}
	return CheckTransports(device, fqcn, m)
}

// CheckAnyShape refuses a device no call of the method could run on: it
// fits neither the manifest's own shape nor any of its Shapes. It is for a
// question asked without a call's parameters (a Controller admitting a
// device for every method a runbook names); a call's own parameters are
// held to their one shape through For and CheckShape. The error is the
// manifest's own shape's.
func CheckAnyShape(device inventory.InventoryItem, fqcn string, m Manifest) error {
	err := CheckShape(device, fqcn, m)
	if err == nil {
		return nil
	}
	for _, s := range m.Shapes {
		if CheckShape(device, fqcn, m.For(map[string]any{s.Param: true})) == nil {
			return nil
		}
	}
	return err
}

// checkShapes refuses a shape that selects by an undocumented, reserved or
// repeated parameter, declares nothing, or names a capability or transport
// the platform does not know.
func checkShapes(d Descriptor) error {
	documented := map[string]bool{}
	for _, p := range d.Manifest.Doc.Params {
		documented[p.Name] = true
	}
	seen := map[string]bool{}
	for _, s := range d.Manifest.Shapes {
		switch {
		case !documented[s.Param]:
			return fmt.Errorf("collection: %q selects a call shape by parameter %q, which it does not document", d.Name, s.Param)
		case IsReservedParam(s.Param):
			return fmt.Errorf("collection: %q selects a call shape by %q, a parameter the engine reads for itself", d.Name, s.Param)
		case seen[s.Param]:
			return fmt.Errorf("collection: %q declares two call shapes selected by %q", d.Name, s.Param)
		case len(s.SupportedTransports) == 0 && len(s.RequiredCapabilities) == 0:
			return fmt.Errorf("collection: %q's call shape selected by %q declares no transport and no capability", d.Name, s.Param)
		}
		seen[s.Param] = true
		for _, name := range s.RequiredCapabilities {
			if _, known := capability.Lookup(name); !known {
				return fmt.Errorf("collection: %q's call shape selected by %q requires unknown capability %q", d.Name, s.Param, name)
			}
		}
		if err := checkTransportNames(Descriptor{Name: d.Name, Manifest: Manifest{SupportedTransports: s.SupportedTransports}}); err != nil {
			return err
		}
	}
	return nil
}
