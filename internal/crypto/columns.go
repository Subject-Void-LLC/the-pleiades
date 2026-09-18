// The one list of columns sealed under the master key, which rotation and the
// census both walk.
package crypto

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/savedlaunchconfig"
)

// encryptedColumn is one database column this package seals under the
// master key, with everything the two whole-database operations need to
// know about it.
//
// Rotation and the setup command's census both have to visit every such
// column, and a column either of them misses is a column whose data is lost:
// rotation leaves it on a key an operator is about to remove, and the census
// reports a database holding it as safe to give a new key. One list serves
// both, and TestEveryEncryptedColumnIsListed pairs it with the write hooks
// this package exports, so a new sealed column cannot land without an entry.
type encryptedColumn struct {
	// noun names the rows in plain words, for a log line or a refusal an
	// operator reads: "credentials", not "credentials.inputs".
	noun string

	// hook names the exported write hook that seals this column.
	hook string

	// table and column name where the ciphertext is stored, taken from
	// ent's generated constants so they cannot drift from the schema.
	table, column string

	// bare is true when the column holds the envelope string itself, as
	// a mesh signing key's seed does. Otherwise the column holds a JSON
	// object whose single key is EncryptedKeyMarker.
	bare bool

	// rotate re-encrypts this column under the current key.
	rotate func(context.Context, *ent.Client, *EnvelopeService) (RotationCount, error)
}

// encryptedColumns lists every column encrypted under the master key. The
// order is the order rotation logs and census refusals list them in.
var encryptedColumns = []encryptedColumn{
	{
		noun: "credentials", hook: "CredentialInputsHook",
		table: credential.Table, column: credential.FieldInputs,
		rotate: RotateCredentialInputs,
	},
	{
		noun: "devices", hook: "DeviceEnvelopePropertiesHook",
		table: device.Table, column: device.FieldProperties,
		rotate: RotateDeviceProperties,
	},
	{
		noun: "saved launch configurations", hook: "SavedLaunchConfigAnswersHook",
		table: savedlaunchconfig.Table, column: savedlaunchconfig.FieldAnswers,
		rotate: RotateSavedLaunchConfigAnswers,
	},
	{
		noun: "mesh signing keys", hook: "MeshSigningKeySeedHook",
		table: meshsigningkey.Table, column: meshsigningkey.FieldSeed,
		bare:   true,
		rotate: RotateMeshSigningKeySeeds,
	},
}
