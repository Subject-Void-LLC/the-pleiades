// Tests of a short SNMP community: the lengths that decide it, and the notice
// every run that uses one gives.
package credential_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestShortCommunity_TheLengthsAgree: the community floor pkg/wire refuses
// under is the named-secret minimum and the length below which the masking
// engine refuses to register a value. Were they to drift, a community the
// masker skips could be stored without the mark, or one it masks could be
// refused.
func TestShortCommunity_TheLengthsAgree(t *testing.T) {
	lengths := []int{wire.MinSNMPCommunityLength, credential.MinSecretLength, redact.MinLiteralLength}
	if slices.Min(lengths) != slices.Max(lengths) {
		t.Errorf("MinSNMPCommunityLength, MinSecretLength and MinLiteralLength are %v: want one number", lengths)
	}
	if !credential.ShortCommunity(map[string]string{wire.SecretSNMPCommunity: "public"}) {
		t.Error("ShortCommunity missed a six-character community")
	}
	for _, secrets := range []map[string]string{{wire.SecretSNMPCommunity: "c0mmunity-s3cret"}, {wire.SecretPassword: "p"}, nil} {
		if credential.ShortCommunity(secrets) {
			t.Errorf("ShortCommunity(%v) = true", len(secrets))
		}
	}
	warning := credential.ShortCommunityWarning("r1")
	if !strings.Contains(warning, `"r1"`) || !strings.Contains(warning, "shape") || !strings.Contains(warning, "never by value") {
		t.Errorf("the warning %q does not name the device and say how it is masked", warning)
	}
}

// TestNoticeShortCommunity_SaysSoOncePerRunAndEveryRun: within one wrapper
// (one run) the first lookup of a device with a short community calls the
// notice and later ones do not; a new wrapper (the next run) says so again,
// since nothing clears it; a device whose community is long, or that has
// none, says nothing; and a failed lookup is returned and says nothing.
func TestNoticeShortCommunity_SaysSoOncePerRunAndEveryRun(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStoreWith(dir, key, "old-switch", credential.Credential{SNMPCommunity: "public"}, credential.SaveOptions{AllowShortCommunity: true}); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "new-switch", credential.Credential{SNMPCommunity: "c0mmunity-s3cret"}); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "server", credential.Credential{Username: "ops", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	var said []string
	run := func() {
		store := credential.NoticeShortCommunity(credential.NewLazyFileStore(dir), func(name string) { said = append(said, name) })
		for _, name := range []string{"old-switch", "old-switch", "new-switch", "server"} {
			if _, err := store.Lookup(context.Background(), name); err != nil {
				t.Fatalf("Lookup(%s): %v", name, err)
			}
		}
	}
	run()
	run()
	if !slices.Equal(said, []string{"old-switch", "old-switch"}) {
		t.Errorf("said %v, want old-switch once in each of two runs", said)
	}
	failing := credential.NoticeShortCommunity(failingLookup{}, func(string) { t.Error("a failed lookup said so") })
	if _, err := failing.Lookup(context.Background(), "old-switch"); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("a failed lookup returned %v", err)
	}
	if _, ok := credential.NoticeShortCommunity(credential.NewLazyFileStore(dir), func(string) {}).(credential.SecretStore); !ok {
		t.Error("the wrapped store answers no named secrets")
	}
}
