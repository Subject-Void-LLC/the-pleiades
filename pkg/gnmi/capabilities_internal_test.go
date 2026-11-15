// Package gnmi: reading a Capabilities answer's models as YANG modules, and
// the digest of the model list.
package gnmi

import (
	"testing"
)

// TestYANGModules_ReadsSRLinuxAndOpenConfigNames reads a URN-qualified name
// (Nokia SR Linux 26.7.2's form, measured 2026-11-15) as its module, a date
// version as a revision, an OpenConfig semantic version as none, and sets
// aside a name holding no module.
func TestYANGModules_ReadsSRLinuxAndOpenConfigNames(t *testing.T) {
	caps := Capabilities{Models: []Model{
		{Name: "urn:nokia.com:srlinux:chassis:interfaces:srl_nokia-interfaces", Organization: "Nokia", Version: "2026-07-31"},
		{Name: "openconfig-interfaces", Organization: "OpenConfig working group", Version: "2.4.1"},
		{Name: "openconfig-interfaces", Organization: "someone else", Version: "2.4.2"},
		{Name: "urn:ietf:params:xml:ns:yang:1:", Version: "2017-02-20"},
	}}
	modules, unread := caps.YANGModules()
	if len(modules) != 2 || modules[0].Name != "openconfig-interfaces" || modules[0].Revision != "" || modules[1].Name != "srl_nokia-interfaces" || modules[1].Revision != "2026-07-31" {
		t.Errorf("modules = %+v, want two, the semantic versions folded into one with no revision", modules)
	}
	if len(unread) != 1 || unread[0] != "urn:ietf:params:xml:ns:yang:1:" {
		t.Errorf("unread = %v", unread)
	}
}

// TestModelsDigest_ChangesWithTheListAlone holds the digest to the list.
func TestModelsDigest_ChangesWithTheListAlone(t *testing.T) {
	a := Capabilities{Models: []Model{{Name: "a", Version: "1"}}}
	b := Capabilities{Models: []Model{{Name: "a", Version: "2"}}}
	if a.ModelsDigest() == b.ModelsDigest() || a.ModelsDigest() != (Capabilities{Version: "other", Models: a.Models}).ModelsDigest() || len(a.ModelsDigest()) != 64 {
		t.Errorf("digests %s and %s", a.ModelsDigest(), b.ModelsDigest())
	}
}
