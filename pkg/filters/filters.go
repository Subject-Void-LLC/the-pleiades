// Package filters provides pure value transforms for runbook expressions.
//
// A filter is not a Collection. A Collection method extends an inventory
// item with a capability-gated <namespace>.<method> action, dispatched
// against a device over a transport. A filter (PLAN.md Section 36) is a
// pure, deterministic function of its arguments, with no device, no
// capability and no execution context. It is never a Task.FQCN. It is
// called from inside an expression (when, when_or, when_cel) and evaluated
// inline, as a sub-expression of a larger condition.
//
// Every function here is plain Go over plain Go types: this package imports
// the standard library and nothing else, so it carries no dependency on the
// expression engine that calls it and can be unit tested as ordinary Go.
// The translation to the engine's own value types lives in exactly one
// place, internal/engine/cel_filters.go, which is also where each function
// is registered under its filters. prefix.
//
// This package performs no I/O of any kind: no file access, no network
// access, no subprocess execution. A filter that did any of those would be
// a smuggled side effect inside what a runbook author reads as a pure value
// transform. Every function bounds the length of its input before parsing
// it (see MaxInputBytes) rather than trusting the engine's own evaluation
// cost limit to catch an oversized argument.
package filters

// MaxInputBytes is the longest flat string any function in this package
// will parse. A longer input is refused up front and takes the caller's
// fallback, without being trimmed, lowercased or handed to strconv.
//
// This bound exists because the expression engine cannot supply one. A
// custom CEL function with no registered cost estimator is charged a flat
// cost of one per call whatever its arguments hold, so a single call on a
// megabyte-long device fact costs the same as a call on "7" as far as the
// engine's cost limit is concerned. The cap has to live here, in the
// function that would otherwise do the parsing.
//
// 4096 was chosen against the widest input any filter in this family
// parses, not picked round: a fully qualified domain name maxes out at 253
// bytes, a CIDR block at under 50, a MAC address at 17, an interface name
// at under 64. That leaves more than an order of magnitude of headroom for
// a legitimate value while keeping a pathological one from ever reaching
// strconv. A filter family that has to accept a whole document rather than
// a flat scalar (a JSON or YAML payload, say) needs its own separate,
// larger, separately justified bound; it must not raise this one.
const MaxInputBytes = 4096
