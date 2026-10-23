// Tests that net.cli.config refuses lines copied from a masked read before
// it reaches the device.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestConfig_RefusesAMaskedSecretBeforeTheDevice: a configuration whose
// secret is a placeholder (as net.ios.config's undo_lines report one) is
// refused naming the placeholder, and no session is opened, so the device
// never has a key set to it.
func TestConfig_RefusesAMaskedSecretBeforeTheDevice(t *testing.T) {
	opened := false
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (cliSession, error) {
		opened = true
		return &fakeSession{}, nil
	})
	masked := netcfg.Mask("configure terminal\nusername admin secret hunter2\nend\n")
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramConfig: masked})
	if err == nil || !strings.Contains(err.Error(), "holds a masked secret") || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("err = %v, want a refusal naming the masked secret", err)
	}
	if opened {
		t.Error("a session was opened for a configuration that was refused")
	}
}
