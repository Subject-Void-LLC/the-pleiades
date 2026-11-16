// Package yang: net.yang.schema's flow against a scripted source, writing
// into a real directory: what is saved and how, what it is masked by, and
// what a check does; the refusals are in refusals_internal_test.go. The
// protocols themselves are proven against real servers in the live and
// restconftest tests.
package yang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestTheProbeIsWhatTheConfigurationMaskerCorrupts is the control the
// byte-for-byte test leans on: the configuration masker, which CVD run 11's
// sources went through, does change the probe. A probe it left alone would
// prove nothing about this method leaving it alone.
func TestTheProbeIsWhatTheConfigurationMaskerCorrupts(t *testing.T) {
	masked, _, err := netcfg.MaskOutput(probeSource)
	if err != nil {
		t.Fatal(err)
	}
	if masked == probeSource {
		t.Fatal("netcfg.MaskOutput leaves the probe as it is, so the probe proves nothing")
	}
}

// TestSchema_SavesEachSourceByteForByte fetches the probe and saves it as the
// device sent it, under the name the library gives, reporting its size and
// digest and no text; a second run finds nothing to change.
func TestSchema_SavesEachSourceByteForByte(t *testing.T) {
	src := probeLibrary()
	withSource(t, src)
	rc := newContext(t)
	rc.secrets = map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "a-long-device-password"}
	res, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe"}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rc.read(t, "yang/r1/pleiades-probe@2026-11-16.yang")
	if !ok || got != probeSource {
		t.Fatalf("saved %q (%v), want the probe byte for byte", got, ok)
	}
	if !res.Changed || rc.stats["via"] != viaNETCONF || rc.stats["dest"] != "yang/r1" || rc.stats["files_changed"] != 1 || rc.stats["bytes"] != len(probeSource) {
		t.Errorf("result %v, stats %v; want one file written over netconf into yang/r1", res, rc.stats)
	}
	records := rc.records(t)
	sum := sha256.Sum256([]byte(probeSource))
	if len(records) != 1 || records[0]["sha256"] != hex.EncodeToString(sum[:]) || records[0]["masked"] != false || records[0]["file"] != "yang/r1/pleiades-probe@2026-11-16.yang" {
		t.Errorf("records = %v, want the probe's digest, unmasked", records)
	}
	for key, value := range rc.stats {
		if text, isText := value.(string); isText && strings.Contains(text, "password") {
			t.Errorf("stat %s holds source text: %q", key, text)
		}
	}
	if !src.closed {
		t.Error("the source was left open")
	}
	again, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe"}})
	if err != nil || again.Changed || rc.writes != 1 {
		t.Errorf("a second run: %v, %v, %d writes; want no change and no write", again, err, rc.writes)
	}
}

// TestSchema_MasksTheRunsSecretsByValueOnly replaces the device's password
// and a named secret wherever they stand, leaves every other word as it was,
// and says nothing of a short secret no source holds.
func TestSchema_MasksTheRunsSecretsByValueOnly(t *testing.T) {
	const password, named = "a-long-device-password", "the-named-psk-value"
	src := probeLibrary()
	src.sources["pleiades-probe@2026-11-16"] = []byte(strings.Replace(probeSource, "the secret key 100", "the secret key 100 "+password+" and "+named+named, 1))
	withSource(t, src)
	rc := newContext(t)
	rc.secrets = map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: password, wire.SecretEnablePassword: "short"}
	rc.known = []string{named, "", named}
	if _, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe@2026-11-16"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := rc.read(t, "yang/r1/pleiades-probe@2026-11-16.yang")
	want := strings.Replace(probeSource, "the secret key 100", "the secret key 100 "+placeholder+" and "+placeholder+placeholder, 1)
	if got != want {
		t.Errorf("saved\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, password) || strings.Contains(got, named) {
		t.Error("a secret value is in the saved source")
	}
	if records := rc.records(t); records[0]["masked"] != true {
		t.Errorf("records = %v, want it marked masked", records)
	}
	if warnings, said := rc.stats[sdk.StatWarnings]; said {
		t.Errorf("warnings = %v, want none: no source holds the short secret", warnings)
	}
	if rc.perms[0] != 0o600 {
		t.Errorf("the source was written %o, want 0600, the owner's alone", rc.perms[0])
	}
}

// TestSchema_RefusesASourceHoldingAShortSecret: a secret under 8 bytes
// cannot be masked without cutting words apart, so a source holding one as a
// word of its own is refused whole, naming the module, and the value is in no
// file, stat or error; the same value inside a longer word is the schema's,
// and saves as served with nothing said. A named secret and a value in its
// YANG-quoted form are found alike.
func TestSchema_RefusesASourceHoldingAShortSecret(t *testing.T) {
	const short = "zq7Kp"
	for name, tc := range map[string]struct {
		text   string
		quoted bool
		named  bool
	}{
		"a password as a word":  {text: `description "the key is ` + short + `, kept here";`},
		"a named secret":        {text: `description "` + short + ` is the key";`, named: true},
		"at the end of a line":  {text: "// " + short},
		"quoted as YANG writes": {text: `description "zq\"7K";`, quoted: true},
	} {
		value := short
		if tc.quoted {
			value = `zq"7K`
		}
		src := probeLibrary()
		src.sources["pleiades-probe@2026-11-16"] = []byte(strings.Replace(probeSource, "  prefix pp;\n", "  prefix pp;\n  "+tc.text+"\n", 1))
		withSource(t, src)
		rc := newContext(t)
		if tc.named {
			rc.known = []string{value}
		} else {
			rc.secrets = map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: value}
		}
		_, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "the source of pleiades-probe@2026-11-16 holds, as a word of its own, a secret value") {
			t.Fatalf("%s: %v, want the probe refused naming the module", name, err)
		}
		if strings.Contains(err.Error(), value) || strings.Contains(fmt.Sprint(rc.stats), value) || rc.writes != 0 {
			t.Errorf("%s: the value reached the error, the stats or a file (%d writes): %v %v", name, rc.writes, err, rc.stats)
		}
	}
	for _, inside := range []string{"x" + short, short + "-status", short + ".sub", "_" + short} {
		src := probeLibrary()
		src.sources["pleiades-probe@2026-11-16"] = []byte(strings.Replace(probeSource, "  prefix pp;\n", "  prefix pp;\n  description \""+inside+"\";\n", 1))
		withSource(t, src)
		rc := newContext(t)
		rc.secrets = map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: short}
		if _, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe"}}); err != nil {
			t.Errorf("%q: %v, want it saved: the value inside a longer word is the schema's", inside, err)
		}
		if _, said := rc.stats[sdk.StatWarnings]; said {
			t.Errorf("%q: a warning was recorded, want none", inside)
		}
	}
}

// TestMaskKnown_FindsASecretAsAQuotedStringWritesIt: a secret holding a quote
// and a backslash stands escaped inside a YANG double-quoted string, and is
// masked there by its own placeholder, as it is where it stands as itself.
func TestMaskKnown_FindsASecretAsAQuotedStringWritesIt(t *testing.T) {
	const secret = `pa"ss\word-123`
	rc := newContext(t)
	rc.known = []string{secret}
	forms, short := maskForms(rc)
	src := "module m { description \"" + `pa\"ss\\word-123` + "\"; reference '" + secret + "'; }"
	got, masked := maskKnown([]byte(src), forms)
	want := "module m { description \"" + placeholder + "\"; reference '" + placeholder + "'; }"
	if len(short) > 0 || !masked || string(got) != want {
		t.Errorf("maskKnown = %q (masked %v, short %v), want both forms masked by one placeholder: %q", got, masked, short, want)
	}
	if same, masked := maskKnown([]byte(probeSource), forms); masked || string(same) != probeSource {
		t.Error("a source holding no secret was changed")
	}
}

// TestCheck_WritesNothing reads every source and predicts the files a run
// would write, writing none.
func TestCheck_WritesNothing(t *testing.T) {
	src := probeLibrary()
	withSource(t, src)
	rc := newContext(t)
	res, err := Check(context.Background(), rc, device("r1", netconfOnly...), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || rc.writes != 0 || rc.stats["files_changed"] != 4 || len(src.fetched) != 4 {
		t.Errorf("check: %v, %d writes, %v changed, %v fetched; want four predicted, all fetched, none written", res, rc.writes, rc.stats["files_changed"], src.fetched)
	}
	if _, ok := rc.read(t, "yang/r1/pleiades-probe@2026-11-16.yang"); ok {
		t.Error("a check wrote a file")
	}
}

// TestSchema_EveryModuleAndItsSubmodules fetches every entry the library
// lists, import-only ones and submodules included, and with submodules off
// leaves the submodules out; naming a submodule fetches it alone.
func TestSchema_EveryModuleAndItsSubmodules(t *testing.T) {
	for _, tc := range []struct {
		params map[string]any
		want   []string
	}{
		{map[string]any{}, []string{"ietf-inet-types@2013-07-15", "pleiades-parent@2024-01-01", "pleiades-child@2024-01-02", "pleiades-probe@2026-11-16"}},
		{map[string]any{paramSubmodules: false}, []string{"ietf-inet-types@2013-07-15", "pleiades-parent@2024-01-01", "pleiades-probe@2026-11-16"}},
		{map[string]any{paramModules: []any{"pleiades-child"}}, []string{"pleiades-child@2024-01-02"}},
		{map[string]any{paramModules: []string{"pleiades-parent", "pleiades-parent@2024-01-01"}}, []string{"pleiades-parent@2024-01-01", "pleiades-child@2024-01-02"}},
	} {
		src := probeLibrary()
		withSource(t, src)
		rc := newContext(t)
		if _, err := Schema(context.Background(), rc, device("r1", netconfOnly...), tc.params); err != nil {
			t.Fatalf("%v: %v", tc.params, err)
		}
		if strings.Join(src.fetched, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%v fetched %v, want %v", tc.params, src.fetched, tc.want)
		}
		for _, r := range rc.records(t) {
			if r["name"] == "pleiades-child" && r["submodule"] != true || r["name"] == "ietf-inet-types" && r["import_only"] != true {
				t.Errorf("%v: record %v is not marked as what it is", tc.params, r)
			}
		}
	}
}
