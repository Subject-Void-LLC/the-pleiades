// Package sdk_test: what an SSH connection notes on its task (a legacy
// algorithm its device's record allows), how warnings merge into a task's
// stats, and the trust-host command a refused host key is advised with.
package sdk_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestConnect_NotesALegacyFlagOnTheTask proves a connection to a device
// whose record allows a legacy SSH algorithm is noted in the task's context,
// once however many connections the task opens, under the device's name,
// and that a context with no collector, a nil connection, and a device that
// allows nothing note nothing.
func TestConnect_NotesALegacyFlagOnTheTask(t *testing.T) {
	srv := startServer(t)
	stub := newStub()
	stub.Props = map[string]any{remoteexec.LegacyKeyExchangeProperty: true}
	device := &sshDevice{Stub: stub, host: srv.Host, port: srv.Port}
	rc := newSecretContext(srv.Secrets())
	params := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}

	ctx := sdk.WithConnectionNotes(context.Background(), "sdk-target")
	for range 2 {
		conn, err := sdk.Connect(ctx, rc, device, params, "test.notes")
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	warnings := sdk.ConnectionWarnings(ctx)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `device "sdk-target" allows legacy SSH key exchange`) || !strings.Contains(warnings[0], "did not need it") {
		t.Errorf("warnings %q, want the one flag the modern server did not need", warnings)
	}
	if w := sdk.ConnectionWarnings(context.Background()); w != nil {
		t.Errorf("a context with no collector returned %q", w)
	}
	sdk.NoteConnection(context.Background(), nil)
	sdk.NoteConnection(ctx, nil)
	if len(sdk.ConnectionWarnings(ctx)) != 1 {
		t.Error("a nil connection was noted")
	}

	plain := &sshDevice{Stub: newStub(), host: srv.Host, port: srv.Port}
	quiet := sdk.WithConnectionNotes(context.Background(), "sdk-target")
	conn, err := sdk.Connect(quiet, rc, plain, params, "test.notes")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if w := sdk.ConnectionWarnings(quiet); len(w) != 0 {
		t.Errorf("a device allowing nothing warned %q", w)
	}
}

// TestMergeWarnings_KeepsWhatAMethodSet proves the warnings a method set stay,
// as a []string or after JSON as a []any, each new one is added once, and
// nothing changes for no warnings or no stats.
func TestMergeWarnings_KeepsWhatAMethodSet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats map[string]any
		want  []string
	}{
		{"none set", map[string]any{}, []string{"b"}},
		{"a list of text", map[string]any{sdk.StatWarnings: []string{"a", "b"}}, []string{"a", "b"}},
		{"after JSON", map[string]any{sdk.StatWarnings: []any{"a", 7}}, []string{"a", "b"}},
	} {
		sdk.MergeWarnings(tc.stats, "b", "b")
		if got := tc.stats[sdk.StatWarnings]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	untouched := map[string]any{}
	sdk.MergeWarnings(untouched)
	if len(untouched) != 0 {
		t.Errorf("no warnings changed the stats: %v", untouched)
	}
	sdk.MergeWarnings(nil, "x")
}

// netconfDevice is an SSH device whose NETCONF listener is at the default
// port.
type netconfDevice struct {
	*sshDevice
}

// NetconfPort reports no port, which means 830.
func (d *netconfDevice) NetconfPort() int { return 0 }

// testKey is a fresh ed25519 host key.
func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestWithTrustAdvice_NamesTheCommandThatAcceptsTheKey proves the trust-host
// advice a refused host key gets: --replace for a changed key, --netconf for
// the NETCONF listener, --reason for a device not flagged ephemeral, and none
// for an unknown key on the NETCONF listener (its callers say how), a revoked
// key, another port, another error or no device.
func TestWithTrustAdvice_NamesTheCommandThatAcceptsTheKey(t *testing.T) {
	presented := testKey(t)
	changed := &remoteexec.HostKeyError{Addr: "192.0.2.1:22", Presented: presented, Known: []knownhosts.KnownKey{{Key: testKey(t), Filename: "kh", Line: 1}}}
	unknown := &remoteexec.HostKeyError{Addr: "192.0.2.1:22", Presented: presented}
	revoked := &remoteexec.HostKeyError{Addr: "192.0.2.1:22", Presented: presented, Revoked: true}

	withProps := func(props map[string]any) *sshDevice {
		stub := newStub()
		stub.Props = props
		return &sshDevice{Stub: stub, host: "192.0.2.1", port: 22}
	}
	device := withProps(nil)
	lab := withProps(map[string]any{sdk.EphemeralProperty: true})
	typed := withProps(map[string]any{sdk.EphemeralProperty: "TRUE"})
	router := &netconfDevice{sshDevice: lab}
	const reason = ` --reason "<why this key is trusted>"`
	for _, tc := range []struct {
		name   string
		err    error
		device inventory.InventoryItem
		port   int
		want   string
	}{
		{"a changed key", changed, device, 22, "replace the old key with: pleiades trust-host sdk-target --first-connect --replace" + reason},
		{"an unknown key", unknown, device, 22, "trust it with: pleiades trust-host sdk-target --first-connect" + reason},
		{"a lab node", changed, lab, 22, "replace the old key with: pleiades trust-host sdk-target --first-connect --replace\n"},
		{"ephemeral as typed", unknown, typed, 22, "trust it with: pleiades trust-host sdk-target --first-connect\n"},
		{"the NETCONF listener changed", changed, router, 830, "pleiades trust-host sdk-target --first-connect --netconf --replace\n"},
		{"the NETCONF listener unknown", unknown, router, 830, ""},
		{"a revoked key", revoked, device, 22, ""},
		{"another port", changed, device, 2222, ""},
		{"another error", net.ErrClosed, device, 22, ""},
		{"a device with no SSH", changed, newStub(), 22, ""},
		{"no device", changed, nil, 22, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sdk.WithTrustAdvice(tc.err, tc.device, tc.port)
			if tc.want == "" {
				if got != tc.err {
					t.Errorf("advice given: %v", got)
				}
				return
			}
			if !strings.Contains(got.Error()+"\n", tc.want) || !strings.Contains(got.Error(), remoteexec.Fingerprint(presented)) {
				t.Errorf("advice %q, want it to say %q and the presented fingerprint", got, tc.want)
			}
		})
	}
	if sdk.IsEphemeral(nil) || sdk.IsEphemeral(device) || !sdk.IsEphemeral(typed) {
		t.Error("IsEphemeral reads a flag wrongly")
	}
}
