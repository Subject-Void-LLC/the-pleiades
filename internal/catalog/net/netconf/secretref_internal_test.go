// Package netconf: tests of secret references in net.netconf.config's
// content, resolved just before the session opens and never recorded.
package netconf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// secretsContext is a stubContext holding named secrets, as the Crawl tier's
// context does, and recording what joined the masking set.
type secretsContext struct {
	*stubContext
	secrets    map[string]string
	registered []string
}

func (c *secretsContext) NamedSecret(name, field string) (string, bool, error) {
	v, ok := c.secrets[name+"."+field]
	if ok {
		c.registered = append(c.registered, v)
	}
	return v, ok, nil
}

func (c *secretsContext) RegisterSecret(values ...string) {
	c.registered = append(c.registered, values...)
}

const keyringDoc = `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><crypto><ikev2 xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-crypto"><keyring><name>KR</name><peer><name>ALL</name><pre-shared-key><key>{{ secret.psk.key }}</key></pre-shared-key></peer></keyring></ikev2></crypto></native>`

func TestConfig_ASecretReferenceIsResolvedEscapedAndNeverRecorded(t *testing.T) {
	const value = "Gx&k<ey-0123456789"
	f := &fakeSession{target: netconf.Running, discovery: nativeDiscovery()}
	useSession(t, f)
	rc := &secretsContext{stubContext: newStubContext(), secrets: map[string]string{"psk.key": value}}
	if _, err := Config(context.Background(), rc, nil, map[string]any{paramContent: keyringDoc, paramBackup: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.editCalls) != 1 || !strings.Contains(f.editCalls[0].Config, "<key>Gx&amp;k&lt;ey-0123456789</key>") {
		t.Fatalf("edit = %v, want the value escaped as XML text", f.editCalls)
	}
	escaped := "Gx&amp;k&lt;ey-0123456789"
	if !contains(rc.registered, value) || !contains(rc.registered, escaped) {
		t.Errorf("masking set = %v, want the raw and the escaped value", rc.registered)
	}
	for k, v := range rc.stats {
		if strings.Contains(fmt.Sprint(v), "Gx&") || strings.Contains(fmt.Sprint(v), escaped) {
			t.Errorf("stat %s holds the secret", k)
		}
	}
}

func TestConfig_ASecretReferenceThatCannotBeResolvedSendsNothing(t *testing.T) {
	for name, rc := range map[string]sdk.RunbookContext{
		"no named secrets":    newStubContext(),
		"no such secret":      &secretsContext{stubContext: newStubContext(), secrets: map[string]string{}},
		"a value with spaces": &secretsContext{stubContext: newStubContext(), secrets: map[string]string{"psk.key": "two words"}},
	} {
		opened := 0
		swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
			opened++
			return &fakeSession{target: netconf.Running, discovery: nativeDiscovery()}, nil
		})
		_, err := Config(context.Background(), rc, nil, map[string]any{paramContent: keyringDoc})
		if err == nil {
			t.Errorf("%s: err = nil, want a refusal", name)
		}
		if name == "no named secrets" && !errors.Is(err, sdk.ErrNoNamedSecrets) {
			t.Errorf("%s: err = %v, want ErrNoNamedSecrets", name, err)
		}
		if opened != 0 {
			t.Errorf("%s: the session was opened for a reference that does not resolve", name)
		}
	}
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
