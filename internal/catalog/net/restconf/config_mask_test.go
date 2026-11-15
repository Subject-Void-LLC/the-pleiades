// Package restconf_test: what net.restconf.config records keeps a device's
// document whole. CVD run 11 (2026-11-15) wrote a DMVPN Tunnel100 whose GRE
// key is 100 to five Catalyst 8000Vs, and every recorded diff came back with
// the 100s of the tunnel's name, its NHRP network-id and its addresses
// replaced by a placeholder, the tunnel's name bare so the document no longer
// parsed. The key leaf is masked in place, as a secret-named leaf; nothing
// else is, while a pre-shared key the same run resolved from a named secret
// is masked everywhere.
package restconf_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// tunnel100 is the run's spoke Tunnel100, as its runbook wrote it.
const tunnel100 = `{"Cisco-IOS-XE-native:Tunnel": [{"name": 100, "description": "DMVPN spoke",
  "ip": {"address": {"primary": {"address": "10.255.100.11", "mask": "255.255.255.0"}},
    "Cisco-IOS-XE-nhrp:nhrp-v4": {"nhrp": {"network-id": 100,
      "nhs": {"ipv4": [{"ipv4": "10.255.100.1", "nbma": {"ipv4": [{"ipv4": "198.51.100.2"}]}}]}}}},
  "Cisco-IOS-XE-tunnel:tunnel": {"source": "GigabitEthernet2", "key": "100"}}]}`

// keyring is the run's IKEv2 keyring, its pre-shared key a named secret.
const keyring = `{"Cisco-IOS-XE-crypto:keyring": [{"name": "DMVPN-KR", "peer": [{"name": "ANY",
  "pre-shared-key": {"key": "{{ secret.dmvpn.psk }}"}}]}]}`

// TestConfig_ATunnelKeyIsMaskedInPlaceAndTheDiffStaysJSON writes the keyring
// and the tunnel, then the tunnel again: each recorded document parses, the
// tunnel's name, network-id and addresses are as written, the key is a
// placeholder, the pre-shared key is in no stat and the run's masking set
// holds no number; the re-run changes nothing, so a masked key does not make
// every run report a change.
func TestConfig_ATunnelKeyIsMaskedInPlaceAndTheDiffStaysJSON(t *testing.T) {
	s := restconftest.New(t, iosxeMode)
	const psk = "Dmvpn-PSK-2026-run11"
	rc := &secretRecorder{recorder: newRecorder(restconftest.Secrets(s)), named: map[string]string{"dmvpn.psk": psk}}
	invoke := func(rc sdk.RunbookContext, path, content string) bool {
		t.Helper()
		res, err := configMethod(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"method": "put", "path": path, "content": content, "backup": true})
		if err != nil {
			t.Fatalf("put %s: %v", path, err)
		}
		return res.Changed
	}
	kr := &secretRecorder{recorder: newRecorder(restconftest.Secrets(s)), named: rc.named}
	if !invoke(kr, "Cisco-IOS-XE-native:native/crypto/Cisco-IOS-XE-crypto:ikev2/keyring=DMVPN-KR", keyring) {
		t.Fatal("the keyring put changed nothing")
	}
	if stored, _ := s.Stored("/restconf/data/Cisco-IOS-XE-native:native/crypto/Cisco-IOS-XE-crypto:ikev2/keyring=DMVPN-KR"); !strings.Contains(string(stored), psk) {
		t.Fatalf("the device stored %s, want the pre-shared key resolved into it", stored)
	}
	if stats, _ := json.Marshal(kr.stats); strings.Contains(string(stats), psk) || !strings.Contains(string(stats), netcfg.Placeholder(psk)) {
		t.Errorf("the keyring's stats = %s, want the pre-shared key masked", stats)
	}
	tunnel := "Cisco-IOS-XE-native:native/interface/Tunnel=100"
	if !invoke(rc, tunnel, tunnel100) {
		t.Fatal("the tunnel put changed nothing")
	}
	again := &secretRecorder{recorder: newRecorder(restconftest.Secrets(s)), named: rc.named}
	if invoke(again, tunnel, tunnel100) {
		t.Error("the same tunnel put again reported a change")
	}
	for name, r := range map[string]*secretRecorder{"first run": rc, "re-run": again} {
		diff, _ := r.stats[sdk.StatDiff].(map[string]any)
		after, _ := diff[sdk.DiffAfter].(map[string]any)
		doc, _ := after["document"].(string)
		if !json.Valid([]byte(doc)) {
			t.Fatalf("%s: the recorded document is not JSON: %s", name, doc)
		}
		for _, kept := range []string{`"name": 100,`, `"network-id": 100`, "10.255.100.11", "10.255.100.1", "198.51.100.2", `"key": "` + netcfg.Placeholder("100") + `"`} {
			if !strings.Contains(doc, kept) {
				t.Errorf("%s: %s is not in the recorded document: %s", name, kept, doc)
			}
		}
		if backup, _ := r.stats["backup"].(string); name == "re-run" && !json.Valid([]byte(backup)) {
			t.Errorf("%s: the backup is not JSON: %s", name, backup)
		}
		stats, _ := json.Marshal(r.stats)
		if strings.Contains(string(stats), psk) {
			t.Errorf("%s: a stat carries the pre-shared key: %s", name, stats)
		}
		for _, v := range r.registered {
			if strings.Trim(v, "0123456789") == "" {
				t.Errorf("%s: the run's masking set holds the number %q", name, v)
			}
		}
	}
}
