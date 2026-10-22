// Tests for a program's capability definitions on the program's own side.
package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// widgetProgram is a program defining acme.widget_api, with one method
// requiring it.
func widgetProgram() external.Program {
	return external.Program{
		Capabilities: []capability.DataDefinition{{
			Name:       "acme.widget_api",
			Properties: []capability.DataProperty{{Name: "endpoint", Type: "string", Required: true}},
		}},
		Methods: []collection.Descriptor{{
			Name: "acme.widget.read",
			Manifest: collection.Manifest{
				Status:               collection.StatusImplemented,
				RequiredCapabilities: []capability.Name{"acme.widget_api"},
				Reversibility:        collection.Reversibility{ReadOnly: true, Notes: "Reads the widget controller and changes nothing."},
			},
			Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				return collection.Result{}, nil
			},
		}},
	}
}

// TestServeProgram_DescribesTheCapabilitiesItDefines: describe lists the
// program's definitions beside its methods.
func TestServeProgram_DescribesTheCapabilitiesItDefines(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	var out, errs bytes.Buffer
	if code := external.ServeProgram(context.Background(), []string{external.CommandDescribe}, widgetProgram(), external.Streams{Out: &out, Err: &errs}); code != 0 {
		t.Fatalf("describe exited %d: %s", code, errs.String())
	}
	var desc external.Description
	if err := json.Unmarshal(out.Bytes(), &desc); err != nil {
		t.Fatal(err)
	}
	if len(desc.Capabilities) != 1 || desc.Capabilities[0].Name != "acme.widget_api" || len(desc.Methods) != 1 {
		t.Errorf("description = %+v, want the capability and the method", desc)
	}
}

// TestServeProgram_RefusesWhatTheLoaderWouldRefuse: a definition outside the
// program's namespaces fails on the author's machine, exit 2, naming why.
func TestServeProgram_RefusesWhatTheLoaderWouldRefuse(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	p := widgetProgram()
	p.Capabilities[0].Name = "other.widget_api"
	p.Methods[0].Manifest.RequiredCapabilities = nil
	var out, errs bytes.Buffer
	if code := external.ServeProgram(context.Background(), []string{external.CommandDescribe}, p, external.Streams{Out: &out, Err: &errs}); code != 2 {
		t.Fatalf("describe exited %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "none of this program's methods uses") {
		t.Errorf("stderr = %q, want it to say why", errs.String())
	}
}
