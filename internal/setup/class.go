// Package setup is the logic behind the controller's setup command: it
// generates the secrets a deployment needs, writes them where that
// deployment reads them, and refuses to replace one that still protects
// data.
//
// The central idea is that the fields this command touches differ by orders
// of magnitude in what getting them wrong costs, so each is classified by
// that cost, in code, and the guard on changing it scales with its class.
// A single --force covering every field would teach an operator one gesture
// and then apply it to a signed-out session and to permanently unreadable
// credentials alike.
//
// Nothing in this package generates key material itself. Every key and
// secret comes from internal/crypto.GenerateKey, the same generator the key
// resolver uses, and internal/archtest holds this package to that.
package setup

// The environment variables this command reads from, and may write to, a
// compose env file. Each name is the one the controller itself reads.
const (
	// VarMasterKey is the master encryption key.
	VarMasterKey = "MASTER_ENCRYPTION_KEY"

	// VarMasterKeyVersion is the version tag the master key carries.
	VarMasterKeyVersion = "MASTER_ENCRYPTION_KEY_VERSION"

	// VarPreviousKey is the key a rotation is moving away from.
	VarPreviousKey = "MASTER_ENCRYPTION_KEY_PREVIOUS"

	// VarPreviousKeyVersion is the version tag of the previous key.
	VarPreviousKeyVersion = "MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION"

	// VarRotate asks the controller to run a rotation pass at startup.
	VarRotate = "ROTATE_ENCRYPTION_KEYS"

	// VarJWTSecret verifies the bearer tokens the API accepts.
	VarJWTSecret = "JWT_SECRET"

	// VarMaxOutage is the longest link outage the mesh must survive.
	VarMaxOutage = "PLEIADES_MAX_OUTAGE"
)

// Class is how much it costs to get a field wrong, which decides the guard
// on changing it.
type Class int

// The classes, from the one nothing recovers to the ones that fix
// themselves.
const (
	// Irreversible fields make stored data permanently unreadable when they
	// are replaced while that data exists. Nothing recovers it. Changing
	// one requires a named flag that states the consequence, and a typed
	// confirmation at a terminal, and is refused outright while any row
	// opens under the key being replaced.
	Irreversible Class = iota + 1

	// OneWayDownward fields may be raised freely, but lowering one makes
	// the broker discard messages it holds. Changing one requires --force,
	// and lowering it is stated as what it discards.
	OneWayDownward

	// Reissue fields invalidate something that is then issued again.
	// Replacing the JWT secret rejects every API token signed with the old
	// one until the token is signed again. Changing one requires --force.
	Reissue

	// Repoint fields name where the data is. Changing one makes existing
	// data look like it vanished until it is pointed back. This command
	// writes one once and never regenerates it.
	Repoint

	// SelfHealing fields regenerate on their own. The TLS certificate is
	// one: a new one costs a browser warning. This command does not touch
	// them.
	SelfHealing
)

// String names the class the way a refusal says it.
func (c Class) String() string {
	switch c {
	case Irreversible:
		return "irreversible"
	case OneWayDownward:
		return "safe to raise, lowering discards messages"
	case Reissue:
		return "reversible, by signing tokens again"
	case Repoint:
		return "reversible, by pointing it back"
	case SelfHealing:
		return "regenerates on its own"
	default:
		return "unclassified"
	}
}

// Field is one value this command reads or writes, with its class.
type Field struct {
	// Name is the variable or setting name, as the target reads it.
	Name string

	// Class is what getting it wrong costs.
	Class Class

	// Written reports whether this command ever writes the field. A field
	// it only reads (a rotation variable an operator set) is still
	// classified, because the guard on rewriting the file around it
	// depends on its class.
	Written bool
}

// Fields lists every field this command touches, in the order the recovery
// matrix prints them. It is the one list both the command and its tests
// read, so a field cannot be written without being classified.
var Fields = []Field{
	{Name: VarMasterKey, Class: Irreversible, Written: true},
	{Name: VarMasterKeyVersion, Class: Irreversible},
	{Name: VarPreviousKey, Class: Irreversible},
	{Name: VarPreviousKeyVersion, Class: Irreversible},
	{Name: VarRotate, Class: Irreversible},
	{Name: VarMaxOutage, Class: OneWayDownward, Written: true},
	{Name: VarJWTSecret, Class: Reissue, Written: true},
	{Name: "DB_DSN", Class: Repoint},
	{Name: "POSTGRES_PASSWORD", Class: Repoint, Written: true},
	{Name: "TLS_CERT_FILE", Class: SelfHealing},
}

// ClassOf returns the class of the named field, and false for a name this
// command does not know.
func ClassOf(name string) (Class, bool) {
	for _, f := range Fields {
		if f.Name == name {
			return f.Class, true
		}
	}
	return 0, false
}

// ComposeVariables lists every variable this command reads from or writes
// to a compose env file. The compose release gate sets each one in the
// process environment of every docker compose command it runs, because the
// shell environment outranks .env, and a developer's own .env must not leak
// into a gate that is meant to start from nothing.
func ComposeVariables() []string {
	return []string{
		VarMasterKey, VarMasterKeyVersion,
		VarPreviousKey, VarPreviousKeyVersion,
		VarRotate, VarJWTSecret, VarMaxOutage,
	}
}
