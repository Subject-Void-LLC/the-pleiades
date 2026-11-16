// Package snmp: one device's SNMP settings, read from its inventory record,
// and what each weakening they allow makes a run say.
package snmp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The properties a device's SNMP is set from.
const (
	// PortProperty is the agent's UDP port, 161 when absent.
	PortProperty = "snmp_port"
	// VersionProperty is "3" (the default), "2c" or "1".
	VersionProperty = "snmp_version"
	// AllowCleartextCommunityProperty must be true for version 1 or 2c, and
	// is refused with version 3, where it would allow nothing.
	AllowCleartextCommunityProperty = "snmp_allow_cleartext_community"
	// AllowLegacyAlgorithmsProperty must be true for an SNMPv3 user that
	// authenticates with MD5 or encrypts with DES.
	AllowLegacyAlgorithmsProperty = "snmp_allow_legacy_algorithms"
	// TimeoutProperty is how many seconds one request waits for its answer.
	TimeoutProperty = "snmp_timeout"
	// RetriesProperty is how many times an unanswered request is sent again.
	RetriesProperty = "snmp_retries"
	// NoBulkProperty walks with GETNEXT only, for an agent whose GETBULK is
	// broken.
	NoBulkProperty = "snmp_no_bulk"
	// MaxRepetitionsProperty is the GETBULK max-repetitions a walk asks for.
	MaxRepetitionsProperty = "snmp_max_repetitions"
	// MinIntervalProperty is the least time, in milliseconds, between two
	// requests to the agent, for one that drops requests sent too fast.
	MinIntervalProperty = "snmp_min_interval_ms"
)

// Properties returns every property this package reads, for a device type's
// dispatch allowlist and its discovery binding.
func Properties() []string {
	return []string{PortProperty, VersionProperty, AllowCleartextCommunityProperty, AllowLegacyAlgorithmsProperty,
		TimeoutProperty, RetriesProperty, NoBulkProperty, MaxRepetitionsProperty, MinIntervalProperty}
}

// Settings is one device's SNMP, validated by Parse.
type Settings struct {
	// Port is the agent's UDP port.
	Port int
	// Version is the version requests are sent in.
	Version Version
	// AllowCleartextCommunity is the operator's acceptance of a community
	// sent in the clear (version 1 or 2c).
	AllowCleartextCommunity bool
	// AllowLegacyAlgorithms is the operator's acceptance of MD5
	// authentication and DES privacy.
	AllowLegacyAlgorithms bool
	// Timeout is how long one request waits for its answer.
	Timeout time.Duration
	// Retries is how many times an unanswered request is sent again.
	Retries int
	// NoBulk walks with GETNEXT only.
	NoBulk bool
	// MaxRepetitions is the GETBULK max-repetitions.
	MaxRepetitions int
	// MinInterval is the least time between two requests.
	MinInterval time.Duration
}

// Defaults returns the settings of a record that sets none: SNMPv3 on port
// 161, no weakening allowed.
func Defaults() Settings {
	return Settings{Port: DefaultPort, Version: Version3, Timeout: DefaultTimeout, Retries: DefaultRetries, MaxRepetitions: DefaultMaxRepetitions}
}

// Configured is implemented by a device whose SNMP is set from its record.
type Configured interface {
	SNMPSettings() Settings
}

// For returns device's SNMP settings, or Defaults for a device that sets
// none.
func For(device any) Settings {
	if c, ok := device.(Configured); ok {
		return c.SNMPSettings()
	}
	return Defaults()
}

// Parse reads and validates the SNMP settings in props. A weakening must be
// asked for by its own flag, and a flag that allows nothing as the record is
// written is refused, since it says the record's author believes something is
// enabled that is not: the cleartext community flag with version 3. Whether
// the legacy algorithm flag allows anything depends on the credential, so
// Check holds it to that.
func Parse(props inventory.Properties) (Settings, error) {
	raw := props.Raw()
	s := Defaults()
	var err error
	if s.Port, err = intProperty(raw, PortProperty, DefaultPort, 1, 65535); err != nil {
		return Settings{}, err
	}
	if v, present := raw[VersionProperty]; present {
		name, ok := versionName(v)
		if !ok || !Version(name).Valid() {
			return Settings{}, fmt.Errorf("property %s must be 3, 2c or 1", VersionProperty)
		}
		s.Version = Version(name)
	}
	if s.AllowCleartextCommunity, err = boolProperty(raw, AllowCleartextCommunityProperty); err != nil {
		return Settings{}, err
	}
	if s.AllowLegacyAlgorithms, err = boolProperty(raw, AllowLegacyAlgorithmsProperty); err != nil {
		return Settings{}, err
	}
	switch {
	case s.Version.Community() && !s.AllowCleartextCommunity:
		return Settings{}, fmt.Errorf("property %s is %s, whose community crosses the network in the clear with every request and authenticates no answer: "+
			"set %s to true to allow it for this device, knowing it is insecure, or configure an SNMPv3 user", VersionProperty, s.Version, AllowCleartextCommunityProperty)
	case s.AllowCleartextCommunity && !s.Version.Community():
		return Settings{}, fmt.Errorf("property %s is true and %s is 3, which sends no community, so it allows nothing: remove it, or set %s to the version this device needs",
			AllowCleartextCommunityProperty, VersionProperty, VersionProperty)
	case s.AllowLegacyAlgorithms && s.Version.Community():
		return Settings{}, fmt.Errorf("property %s applies to an SNMPv3 user, and this device speaks SNMPv%s: remove it", AllowLegacyAlgorithmsProperty, s.Version)
	}
	timeout, err := intProperty(raw, TimeoutProperty, int(DefaultTimeout/time.Second), 1, int(MaxTimeout/time.Second))
	if err != nil {
		return Settings{}, err
	}
	s.Timeout = time.Duration(timeout) * time.Second
	if s.Retries, err = intProperty(raw, RetriesProperty, DefaultRetries, 0, MaxRetries); err != nil {
		return Settings{}, err
	}
	if s.NoBulk, err = boolProperty(raw, NoBulkProperty); err != nil {
		return Settings{}, err
	}
	if s.MaxRepetitions, err = intProperty(raw, MaxRepetitionsProperty, DefaultMaxRepetitions, 1, MaxMaxRepetitions); err != nil {
		return Settings{}, err
	}
	interval, err := intProperty(raw, MinIntervalProperty, 0, 0, int(MaxMinInterval/time.Millisecond))
	if err != nil {
		return Settings{}, err
	}
	s.MinInterval = time.Duration(interval) * time.Millisecond
	switch {
	case s.NoBulk && s.Version == Version1:
		return Settings{}, fmt.Errorf("property %s is true and SNMPv1 has no GETBULK to turn off: remove it", NoBulkProperty)
	case (s.NoBulk || s.Version == Version1) && present(raw, MaxRepetitionsProperty):
		return Settings{}, fmt.Errorf("property %s sizes a GETBULK, and this device's walks use GETNEXT only: remove it", MaxRepetitionsProperty)
	}
	return s, nil
}

// Check refuses cred for a device with settings s, before anything is sent:
// a credential of the other version's kind, an empty one, a legacy algorithm
// the device does not allow, the legacy flag with a credential that uses
// none, and 3DES, which nothing here implements (ErrUnsupportedPrivacy). The
// credential itself was held to wire.SNMPCredential.Check when it was stored.
func (s Settings) Check(cred wire.SNMPCredential) error {
	switch {
	case s.Version.Community() && cred.Level() != "":
		return fmt.Errorf("the device speaks SNMPv%s and its credential holds an SNMPv3 user; store a community for it, or set %s to 3", s.Version, VersionProperty)
	case s.Version.Community() && cred.Community == "":
		return fmt.Errorf("the device speaks SNMPv%s and its credential holds no community; store one with pleiades add-credential --snmp-community", s.Version)
	case !s.Version.Community() && cred.Community != "":
		return fmt.Errorf("the device speaks SNMPv3 and its credential holds a community; store an SNMPv3 user for it, or set %s to 2c or 1 with %s", VersionProperty, AllowCleartextCommunityProperty)
	case !s.Version.Community() && cred.User == "":
		return fmt.Errorf("the device speaks SNMPv3 and its credential holds no SNMPv3 user; store one with pleiades add-credential --snmp-user")
	case cred.PrivProtocol == wire.SNMPPriv3DES:
		return ErrUnsupportedPrivacy
	}
	legacy := LegacyAlgorithms(cred)
	switch {
	case len(legacy) > 0 && !s.AllowLegacyAlgorithms:
		return fmt.Errorf("the device's SNMPv3 user uses %s, which is broken and gone from current devices: set %s to true to allow it for this device, knowing it is insecure, or reconfigure the user with sha256 and aes128",
			strings.Join(legacy, " and "), AllowLegacyAlgorithmsProperty)
	case len(legacy) == 0 && s.AllowLegacyAlgorithms:
		return fmt.Errorf("property %s is true and the device's SNMPv3 user uses no legacy algorithm, so it allows nothing: remove it", AllowLegacyAlgorithmsProperty)
	}
	return nil
}

// LegacyAlgorithms names the old algorithms cred uses: md5 authentication,
// and des or 3des privacy. It is empty for a community or a current user.
func LegacyAlgorithms(cred wire.SNMPCredential) []string {
	var out []string
	if cred.AuthProtocol == wire.SNMPAuthMD5 {
		out = append(out, "md5 authentication")
	}
	if cred.PrivProtocol == wire.SNMPPrivDES || cred.PrivProtocol == wire.SNMPPriv3DES {
		out = append(out, string(cred.PrivProtocol)+" privacy")
	}
	return out
}

// Warnings says, in words a person acts on, what each weakening s and cred
// allow means for the named device: a community in the clear, a legacy
// algorithm, and an SNMPv3 user at a level below authPriv. It is empty for an
// authPriv user with current algorithms.
func (s Settings) Warnings(device string, cred wire.SNMPCredential) []string {
	var out []string
	if s.Version.Community() {
		out = append(out, fmt.Sprintf("device %q is reached over SNMPv%s (%s): its community crosses the network in the clear with every request, and nothing authenticates the agent's answers; keep it only while the device cannot speak SNMPv3",
			device, s.Version, AllowCleartextCommunityProperty))
	}
	if legacy := LegacyAlgorithms(cred); len(legacy) > 0 && s.AllowLegacyAlgorithms {
		out = append(out, fmt.Sprintf("device %q's SNMPv3 user uses %s (%s): broken algorithms, gone from current devices; keep them only while the device cannot be upgraded",
			device, strings.Join(legacy, " and "), AllowLegacyAlgorithmsProperty))
	}
	switch cred.Level() {
	case wire.SNMPNoAuthNoPriv:
		out = append(out, fmt.Sprintf("device %q's SNMPv3 user is noAuthNoPriv: nothing authenticates the agent's answers and nothing is encrypted", device))
	case wire.SNMPAuthNoPriv:
		out = append(out, fmt.Sprintf("device %q's SNMPv3 user is authNoPriv: the agent's answers are authenticated and cross the network unencrypted", device))
	}
	return out
}

// Authenticated reports whether a session with cred proves which agent
// answered: an SNMPv3 user that authenticates (authNoPriv or authPriv). A
// community, or a noAuthNoPriv user, authenticates nothing the agent says.
func Authenticated(cred wire.SNMPCredential) bool {
	level := cred.Level()
	return level == wire.SNMPAuthNoPriv || level == wire.SNMPAuthPriv
}

// present reports whether key is set in raw.
func present(raw map[string]inventory.PropertyValue, key string) bool {
	_, ok := raw[key]
	return ok
}

// boolProperty reads key as a boolean, false when absent; anything else,
// "yes" included, is refused.
func boolProperty(raw map[string]inventory.PropertyValue, key string) (bool, error) {
	v, ok := raw[key]
	if !ok {
		return false, nil
	}
	b, isBool := v.(bool)
	if !isBool {
		return false, fmt.Errorf("property %s must be true or false", key)
	}
	return b, nil
}

// intProperty reads key as a whole number in [lowest, highest], fallback
// when absent. YAML hands a number over as an int, JSON as a float64; a
// fractional one is refused.
func intProperty(raw map[string]inventory.PropertyValue, key string, fallback, lowest, highest int) (int, error) {
	v, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case float64:
		if x != float64(int(x)) {
			return 0, fmt.Errorf("property %s must be a whole number", key)
		}
		n = int(x)
	default:
		return 0, fmt.Errorf("property %s must be a number", key)
	}
	if n < lowest || n > highest {
		return 0, fmt.Errorf("property %s must be from %d to %d", key, lowest, highest)
	}
	return n, nil
}

// versionName reads snmp_version, which YAML may hand over as text ("2c",
// "3") or as a number (3).
func versionName(v inventory.PropertyValue) (string, bool) {
	switch x := v.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(x)), true
	case int:
		return strconv.Itoa(x), true
	case float64:
		if x != float64(int(x)) {
			return "", false
		}
		return strconv.Itoa(int(x)), true
	}
	return "", false
}
