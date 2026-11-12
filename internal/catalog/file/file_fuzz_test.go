// FuzzCopyParams and FuzzDirectoryParams are file.copy's and
// file.directory's parameter fuzz targets, which Phase 38 requires before a
// status flips, in the shape of exec's FuzzCommandParams.
//
// The boundary is the destination path, the content or src, and the mode,
// owner and group, each out of a runbook file and parsed before anything
// connects. The device is nil, so any input that parses stops at the
// connection, which keeps every iteration to microseconds and off the
// network. The property is that no bytes ever panic and that a result never
// claims a change beside an error. The owner and group seeds include
// values beginning with a dash, the hole the Phase 38 audit named in these
// two methods (FAILURE_PATTERNS 1174), and so a second property holds: an
// owner or group pkg/operand refuses never gets as far as the connection,
// whatever else the params hold.
package file_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// refusedBeforeConnecting fails t when an owner or group pkg/operand
// refuses got as far as the connection, which with no device fails saying
// there is none.
func refusedBeforeConnecting(t *testing.T, err error, owner, group string) {
	t.Helper()
	if operand.Problem(owner) == "" && operand.Problem(group) == "" {
		return
	}
	if strings.Contains(err.Error(), "no target device") {
		t.Fatalf("owner %q or group %q, which the operand rule refuses, reached the connection: %v", owner, group, err)
	}
}

// fuzzContext is the smallest sdk.RunbookContext a parse-stage fuzz needs:
// no secrets, and stats it keeps nowhere.
type fuzzContext struct{}

func (fuzzContext) InjectSecrets() map[string]string { return nil }
func (fuzzContext) SetStat(string, any) error        { return nil }
func (fuzzContext) EmitFact(key string, v any) error { return nil }

// fuzzFileCall runs one file method with no device and holds it to the
// parse-stage contract: an error, and no change claimed beside it.
func fuzzFileCall(t *testing.T, fn func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error), params map[string]any) error {
	t.Helper()
	result, err := fn(context.Background(), fuzzContext{}, nil, params)
	if err == nil {
		t.Fatalf("a file method with no target device returned no error for params %+v", params)
	}
	if result.Changed {
		t.Fatalf("a file method reported a change alongside an error for params %+v", params)
	}
	return err
}

// FuzzCopyParams is file.copy's parameter fuzz target.
func FuzzCopyParams(f *testing.F) {
	f.Add("/etc/app.conf", "hello", "0644", "root", "root")
	f.Add("", "", "", "", "")
	f.Add("-dest", "content", "u+x", "-owner", "-group")
	f.Add("/x", "\x00\n'", "99999", "--reference=/etc/shadow", "1000")
	f.Add("/x", "data", "0600", "nginx*", "ng?nx")
	// Each refusal alone, beside params that otherwise reach the connection.
	f.Add("/x", "data", "0600", "--reference=/etc/shadow", "root")
	f.Add("/x", "data", "0600", "root", "--reference=/etc/shadow")
	f.Add("/x", "data", "", "-R", "")
	f.Add("/x", "data", "", "", "web[12]")
	f.Add("/x", "data", "", "www-data", "app.svc")

	f.Fuzz(func(t *testing.T, dest, content, mode, owner, group string) {
		err := fuzzFileCall(t, file.Copy, map[string]any{
			"dest":                          dest,
			"content":                       content,
			"mode":                          mode,
			"owner":                         owner,
			"group":                         group,
			"insecure_skip_host_key_verify": true,
		})
		refusedBeforeConnecting(t, err, owner, group)
	})
}

// FuzzDirectoryParams is file.directory's parameter fuzz target.
func FuzzDirectoryParams(f *testing.F) {
	f.Add("/srv/data", "0750", "root", "root")
	f.Add("", "", "", "")
	f.Add("-path", "u+rwx", "-owner", "-group")
	f.Add("/x", "99999", "--reference=/etc/shadow", "1000")
	f.Add("/x", "0700", "nginx*", "ng?nx")
	// Each refusal alone, beside params that otherwise reach the connection.
	f.Add("/x", "0700", "--reference=/etc/shadow", "root")
	f.Add("/x", "0700", "root", "--reference=/etc/shadow")
	f.Add("/x", "", "-R", "")
	f.Add("/x", "", "", "web[12]")
	f.Add("/x", "", "www-data", "app.svc")

	f.Fuzz(func(t *testing.T, path, mode, owner, group string) {
		err := fuzzFileCall(t, file.Directory, map[string]any{
			"path":                          path,
			"mode":                          mode,
			"owner":                         owner,
			"group":                         group,
			"insecure_skip_host_key_verify": true,
		})
		refusedBeforeConnecting(t, err, owner, group)
	})
}
