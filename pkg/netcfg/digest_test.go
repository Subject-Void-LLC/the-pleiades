// Package netcfg_test: the running-configuration digests' normalization. A line
// that changes on its own must not change the digest; a line someone wrote
// must.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

const iosConfig = "Building configuration...\r\n\r\nCurrent configuration : 1234 bytes\r\n!\r\n! Last configuration change at 10:00:00 UTC Mon Nov 17 2026 by admin\r\n! NVRAM config last updated at 09:00:00 UTC Mon Nov 17 2026\r\n!\r\nhostname r1   \r\nntp clock-period 17179868\r\ninterface Gi1\r\n ip address 10.0.0.1 255.255.255.0\r\nend\r\n"

func TestNormalizeIOS_VolatileLinesDoNotChangeTheDigest(t *testing.T) {
	later := "Building configuration...\n\nCurrent configuration : 1240 bytes\n!\n! Last configuration change at 11:30:00 UTC Tue Nov 18 2026 by ops\n! No configuration change since last restart\n!\nhostname r1\nntp clock-period 17179870\ninterface Gi1\n ip address 10.0.0.1 255.255.255.0\nend\n"
	a := netcfg.RunningDigest(netcfg.NormalizeIOS(iosConfig), netcfg.SchemeIOSText)
	b := netcfg.RunningDigest(netcfg.NormalizeIOS(later), netcfg.SchemeIOSText)
	if a[netcfg.RawDigestField] != b[netcfg.RawDigestField] {
		t.Fatalf("an IOS configuration whose only change is volatile lines digests differently:\n%q\n%q", netcfg.NormalizeIOS(iosConfig), netcfg.NormalizeIOS(later))
	}
}

func TestNormalizeIOS_AWrittenLineChangesTheDigest(t *testing.T) {
	changed := "hostname r1\ninterface Gi1\n ip address 10.0.0.2 255.255.255.0\nend\n"
	a := netcfg.RunningDigest(netcfg.NormalizeIOS(iosConfig), netcfg.SchemeIOSText)
	b := netcfg.RunningDigest(netcfg.NormalizeIOS(changed), netcfg.SchemeIOSText)
	if a[netcfg.RawDigestField] == b[netcfg.RawDigestField] {
		t.Fatal("a changed address did not change the digest")
	}
}

func TestNormalizeFRR_DropsOnlyTheBanner(t *testing.T) {
	got := netcfg.NormalizeFRR("Building configuration...\n\nCurrent configuration:\n!\nfrr version 10.1.1\nhostname r1\n!\nend\n")
	want := "!\nfrr version 10.1.1\nhostname r1\n!\nend\n"
	if got != want {
		t.Fatalf("NormalizeFRR = %q, want %q", got, want)
	}
}

func TestRunningDigest_HoldsARawDigestAndNoSize(t *testing.T) {
	v := netcfg.RunningDigest("hostname r1\n", netcfg.SchemeFRRText)
	raw, ok := v[netcfg.RawDigestField].(string)
	if !ok || len(raw) != 64 {
		t.Fatalf("raw digest = %v, want 64 hex characters", v[netcfg.RawDigestField])
	}
	if v["scheme"] != netcfg.SchemeFRRText || v["datastore"] != "running" {
		t.Fatalf("value = %+v, want its scheme and datastore", v)
	}
	for _, k := range []string{"bytes", "size", "length"} {
		if _, has := v[k]; has {
			t.Errorf("the digest value holds %s, which a secret-bearing configuration need not give away", k)
		}
	}
}

func TestNormalize_AnEmptyConfigurationIsEmpty(t *testing.T) {
	if got := netcfg.NormalizeIOS("\r\n\r\nBuilding configuration...\r\n\r\n"); got != "" {
		t.Fatalf("NormalizeIOS(banner only) = %q, want empty", got)
	}
}

// TestRawDigestField_IsTheCollectionsField: the engine keys only what sits under
// collection.RawDigestField, so a drift here would hand over a digest nothing
// keys.
func TestRawDigestField_IsTheCollectionsField(t *testing.T) {
	if netcfg.RawDigestField != collection.RawDigestField {
		t.Fatalf("netcfg.RawDigestField = %q, collection.RawDigestField = %q", netcfg.RawDigestField, collection.RawDigestField)
	}
}

// FuzzNormalize holds both platforms' normalization to its rules for any text:
// normalizing twice changes nothing, so a digest never depends on how many
// times a configuration was read; and the result holds no carriage return and
// no line ending in space.
func FuzzNormalize(f *testing.F) {
	f.Add(iosConfig)
	f.Add("Building configuration...\n\nCurrent configuration:\n!\nfrr version 10.1.1\nhostname r1\n!\nend\n")
	f.Add("\r\n\r\n")
	f.Add("hostname r1 \t\r\n! Last configuration change at x\r\n")
	f.Fuzz(func(t *testing.T, config string) {
		for name, norm := range map[string]func(string) string{"ios": netcfg.NormalizeIOS, "frr": netcfg.NormalizeFRR} {
			once := norm(config)
			if twice := norm(once); twice != once {
				t.Fatalf("%s: normalizing twice changed the text:\n%q\n%q", name, once, twice)
			}
			for _, line := range strings.Split(once, "\n") {
				if strings.ContainsRune(line, '\r') || strings.TrimRight(line, " \t") != line {
					t.Fatalf("%s: a normalized line holds a carriage return or trailing space: %q", name, line)
				}
			}
		}
	})
}
