// Package restconf_test: the client against restconftest's IOS XE mode, which
// answers exactly as a Catalyst 8000V running IOS XE 17.15.1a did on
// 2026-11-15: host-meta only to a valid login (404 otherwise), so the client
// sends it none and takes /restconf, which the authenticated read confirms.
package restconf_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// iosxe is the IOS XE mode's login.
var iosxe = restconftest.Options{IOSXE: true, Username: "admin", Password: "iosxe-Restconf-5"}

func TestIOSXE_DiscoversTheRouterWithNoCredentialAtHostMeta(t *testing.T) {
	s := restconftest.New(t, iosxe)
	d, _, err := discover(t, s, restconftest.NewDevice(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if d.Root != "/restconf" || d.RootSource != restconf.RootDefault {
		t.Errorf("Root = %q from %q, want /restconf from the conventional default", d.Root, d.RootSource)
	}
	if s.HostMetaLogins() != 0 {
		t.Errorf("host-meta was sent the credential %d times, want never", s.HostMetaLogins())
	}
	if len(d.Protocol) != 11 || d.CapabilitiesError != "" {
		t.Errorf("Protocol = %d capabilities (%q), want the bare container's 11", len(d.Protocol), d.CapabilitiesError)
	}
	if d.Completeness != yanglib.Complete || len(d.Modules) < 500 || strings.Join(d.Datastores, ",") != "intended,operational,running" {
		t.Errorf("Discovery = %s, %d modules, datastores %v", d.Completeness, len(d.Modules), d.Datastores)
	}
}

// TestIOSXE_AWrongPasswordIsSentOnce: host-meta answers 404 to a wrong login
// as to none, so the client sends it none; the first read under /restconf
// answers 401, and nothing more is sent.
func TestIOSXE_AWrongPasswordIsSentOnce(t *testing.T) {
	s := restconftest.New(t, iosxe)
	secrets := restconftest.Secrets(s)
	secrets[wire.SecretPassword] = "not-the-password"
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", secrets)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Discover(context.Background())
	var refused *restconf.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Discover = %v, want a *RefusedError", err)
	}
	if s.Logins() != 1 || s.HostMetaLogins() != 0 {
		t.Errorf("the wrong password was sent %d times (%d to host-meta), want once and never to host-meta", s.Logins(), s.HostMetaLogins())
	}
}

// TestIOSXE_ReadsAndWritesItsOwnShapes: a post answered 201 with an absolute
// Location is read back as a path under /data; a second post is 409
// data-exists; an NMDA operational read with nothing in it (204) and a
// missing entry (404 invalid-value) are absent.
func TestIOSXE_ReadsAndWritesItsOwnShapes(t *testing.T) {
	s := restconftest.New(t, iosxe)
	client, _, err := restconf.ClientFor(restconftest.NewDevice(t, s), "router", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.DiscoverRoot(ctx); err != nil {
		t.Fatal(err)
	}
	parent := restconf.Path{restconf.Node("Cisco-IOS-XE-native", "native"), restconf.Node("", "interface")}
	loopback := []byte(`{"Cisco-IOS-XE-native:Loopback": [{"name": 7701, "description": "rc-capture"}]}`)
	w, err := client.Write(ctx, restconf.MethodPost, "", parent, restconf.MediaTypeJSON, loopback)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Location.Escaped(); w.Status != 201 || got != "Cisco-IOS-XE-native:native/interface/Loopback=7701" {
		t.Errorf("post = %d, Location %q", w.Status, got)
	}
	_, err = client.Write(ctx, restconf.MethodPost, "", parent, restconf.MediaTypeJSON, loopback)
	var status *restconf.StatusError
	if !errors.As(err, &status) || status.Status != 409 || !status.HasTag("data-exists") {
		t.Errorf("a second post = %v, want 409 data-exists", err)
	}
	if _, found, err := client.Read(ctx, "", restconf.Path{restconf.Node("Cisco-IOS-XE-native", "native"), restconf.Node("", "interface"), restconf.Node("", "Loopback").Entry("9999")}, restconf.MediaTypeJSON); err != nil || found {
		t.Errorf("a missing entry read = %v, %v; want absent", found, err)
	}
}
