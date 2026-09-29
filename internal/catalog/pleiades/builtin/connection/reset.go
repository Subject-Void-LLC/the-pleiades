// Package connection implements the "pleiades.builtin.connection.reset"
// Collection method: close the SSH connection a run keeps open to a
// device, so the device's next task logs in again. It is Ansible's
// `meta: reset_connection`.
//
// # Why a method and not an engine keyword
//
// A registered method gets validation, check mode, reference docs and
// dispatch on both tiers from the one registry every other method uses,
// where a new engine keyword would need each of those added by hand. It
// needs nothing an ordinary method lacks: the run's connection pool
// reaches it through its RunbookContext (sdk.ConnectionPooler), exactly
// as it reaches sdk.Connect.
//
// Its manifest also declares EndsLoginSession, which is what closes the
// connection on the Walk tier, where the pool lives in the per-dispatch
// child rather than in this method's process, and what closes it after
// the identity methods on both tiers.
//
// Because this package lives under internal/, it is reachable only from
// code inside this module or a fork of it: Go's internal/ visibility rule
// blocks any other module from importing it at all.
package connection

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "pleiades.builtin.connection.reset",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameSSHTransport,
			},
			// Closing a connection on the platform's side needs no
			// privilege on the device.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "It changes nothing on the device: it closes the platform's own connection, and the next task opens a new one, " +
					"so the device is as it would have been had the task never run and there is nothing to undo.",
				ReadOnly: true,
			},
			// Its check is its run: it only closes a connection, which a
			// check may do as freely as a run, since the next task logs
			// in again either way.
			SupportsCheck:    true,
			EndsLoginSession: true,
			Doc: collection.Doc{
				Summary:     "Closes the SSH connection kept open to the device, so its next task logs in again.",
				Description: "A run keeps one SSH connection per device open between tasks unless persist_connections is turned off. A login made before a change to the account it logs in as does not see that change: a group the account was just added to, a new shell, a changed limit. The identity methods close the connection themselves after they run, so this is for the change they cannot see, such as a group membership edited by exec.command. It is Ansible's meta: reset_connection, and migrate-playbook converts that to it. It touches nothing on the device, so it never reports changed, and a check runs it as it is. Where connections do not persist it does nothing, since every task already logs in afresh.",
				Examples: []collection.Example{
					{Name: "Pick up a group membership in the next task", RunbookYAML: "- name: Add deploy to the docker group\n  exec.command:\n    cmd: usermod -aG docker deploy\n- name: Log in again, so the new group applies\n  pleiades.builtin.connection.reset: {}\n- name: Use docker as deploy\n  exec.command:\n    cmd: docker ps\n"},
				},
				SeeAlso: []string{"identity.user.modify", "identity.group.modify", "exec.command"},
			},
		},
		Invoke: Reset,
		Check:  Reset,
	})
}

// Reset closes device's pooled connection, when rc lends from a pool, and
// reports no change. With no pool there is nothing kept open to close.
func Reset(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	if p, ok := rc.(sdk.ConnectionPooler); ok && device != nil {
		if pool := p.ConnectionPool(); pool != nil {
			pool.Discard(device.Name())
		}
	}
	return collection.Result{Changed: false}, nil
}
