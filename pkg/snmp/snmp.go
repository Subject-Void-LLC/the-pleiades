// Package snmp is this platform's SNMP client: GET, GETNEXT, GETBULK, a
// bounded walk and SET, over SNMPv1, SNMPv2c and SNMPv3 with the User-based
// Security Model at every security level, built on github.com/gosnmp/gosnmp
// (BSD-2-Clause, with a part derived from the Go standard library's ASN.1
// code under BSD-3-Clause).
//
// It is not a transport.Transport. An SNMP request has no stdout, no stderr
// and no exit code, so the command-shaped port would describe nothing true
// about it; the net.snmp.* methods, wait.snmp and onboarding call this
// package directly, as pkg/restconf's callers do, and build their client
// through ClientFor so they can never disagree about how a device is reached.
//
// # Every version, the secure one by default
//
// SNMPv3 is the default and the only version reached without a word from the
// operator. SNMPv1 and SNMPv2c authenticate with a community string sent in
// the clear in every request and protect nothing they carry, and some devices
// speak nothing else, so they are reached only through a per-device opt-in
// (AllowCleartextCommunityProperty) that is warned about wherever the device
// is used, as pkg/devicetls warns about deprecated TLS. Inside SNMPv3 the old
// algorithms, MD5 authentication and DES or 3DES privacy, are behind a second
// opt-in of their own (AllowLegacyAlgorithmsProperty), so one weakening never
// brings the next with it.
//
// # What is proven, and what is not
//
// Every authentication protocol the credential model names (MD5, SHA and
// SHA-224/256/384/512) and every privacy protocol but 3DES (DES, AES-128,
// AES-192 and AES-256 in both key-extension schemes, Blumenthal and Reeder) is
// proven against a real net-snmp agent by this package's container tests.
// 3DES (draft-reeder-snmpv3-usm-3desede) is refused before anything is sent:
// gosnmp does not implement it, and net-snmp 5.9.4, the agent the tests run,
// does not either, so nothing here could send it or prove it.
//
// # Bounds
//
// Every answer is one UDP datagram, which gosnmp reads into a 64 KiB buffer
// and refuses when it fills it, so no answer can announce its way past that
// before it is decoded. A walk is bounded by a count of variable bindings
// (MaxWalkResults at most) and by MaxWalkBytes of values, and it refuses an
// agent that answers an OID not after the one it asked from, which would
// otherwise walk forever.
package snmp

import (
	"errors"
	"fmt"
	"time"
)

// Version is the SNMP version a device is reached with.
type Version string

// The versions. The names are the ones net-snmp's tools take after -v.
const (
	Version1  Version = "1"
	Version2c Version = "2c"
	Version3  Version = "3"
)

// Community reports whether v authenticates with a community (v1 and v2c).
func (v Version) Community() bool { return v == Version1 || v == Version2c }

// Valid reports whether v is one of the three versions.
func (v Version) Valid() bool { return v == Version1 || v == Version2c || v == Version3 }

// The bounds on what one client asks and accepts.
const (
	// DefaultPort is the agent port RFC 3417 assigns.
	DefaultPort = 161

	// MaxOIDsPerRequest is the most OIDs one GET or SET names. It is
	// gosnmp's own default, which most agents accept in one datagram.
	MaxOIDsPerRequest = 60

	// MaxWalkResults is the most variable bindings a walk returns.
	MaxWalkResults = 10000

	// MaxWalkBytes bounds the values a walk keeps, summed over its
	// bindings, so a table of large strings cannot fill memory.
	MaxWalkBytes = 8 << 20

	// DefaultTimeout is how long one request waits for its answer before it
	// is sent again or fails.
	DefaultTimeout = 5 * time.Second

	// MaxTimeout bounds DefaultTimeout's setting.
	MaxTimeout = 2 * time.Minute

	// DefaultRetries is how many times one request is sent again after its
	// first send went unanswered.
	DefaultRetries = 1

	// MaxRetries bounds DefaultRetries' setting.
	MaxRetries = 10

	// DefaultMaxRepetitions is how many bindings one GETBULK asks for per
	// OID. It is lower than gosnmp's 50 because some agents answer a large
	// GETBULK with tooBig or nothing at all.
	DefaultMaxRepetitions = 10

	// MaxMaxRepetitions bounds the max-repetitions setting.
	MaxMaxRepetitions = 100

	// MaxMinInterval bounds the minimum interval between requests.
	MaxMinInterval = 10 * time.Second
)

// ErrUnsupportedPrivacy is the refusal of a privacy protocol the credential
// model accepts and no client here can send (3DES).
var ErrUnsupportedPrivacy = errors.New("3des privacy (draft-reeder-snmpv3-usm-3desede) is not implemented by this client (gosnmp has no 3DES-EDE) " +
	"and no agent this platform tests against implements it either, so it is refused before anything is sent; configure the user with aes128 or another AES protocol")

// RefusedError is an agent's refusal of the client's credential that sending
// it again cannot change: an SNMPv3 report of an unknown user, a wrong
// digest, a security level the user is not configured for, or a message it
// could not decrypt. A wait stops on one rather than send a credential that
// can lock an account again and again. A community the agent does not know
// is never one of these: an SNMPv1 or SNMPv2c agent drops such a request
// without an answer, which reads as a timeout.
type RefusedError struct {
	// Reason is what the agent reported, in words.
	Reason string
}

// Error names the refusal.
func (e *RefusedError) Error() string { return "the agent refused the credential: " + e.Reason }

// AgentError is an agent's answer that carries an SNMP error status (RFC
// 3416 section 3): noSuchName, notWritable, wrongType and the rest.
type AgentError struct {
	// Status is the error status's RFC name, such as notWritable.
	Status string
	// Index is the 1-based position of the variable binding at fault, or 0.
	Index int
	// OID is that binding's OID, when Index names one.
	OID string
}

// Error names the status and the binding at fault.
func (e *AgentError) Error() string {
	if e.OID != "" {
		return fmt.Sprintf("the agent answered %s for %s", e.Status, e.OID)
	}
	return "the agent answered " + e.Status
}
