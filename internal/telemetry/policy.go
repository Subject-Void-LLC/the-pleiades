// Package telemetry: the attribute policy, the fixed list of what a span
// may carry out of this process.
package telemetry

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// allowedAttributeKeys is the fixed allowlist of attribute keys that may
// leave this process on a span, an event, a link, an instrumentation scope
// or the resource. PLAN.md Section 19 item 7 asks for a security policy over
// what a span may carry. The boundary is that telemetry carries identifiers
// (ids, names, routes, subjects, kinds, status codes, counts, versions and
// fixed-phrase outcomes) and never values (params, extra vars, survey
// answers, credentials, device output, command text, bodies, headers, query
// strings, environment variables, or error text built from any of these). A
// closed list of keys is the only form of that rule a reviewer can read in
// one screen.
//
// It is one list for every location rather than one per location, because a
// key that is safe on a span is safe on an event, and five lists would be
// five places to forget.
//
// exception.message and exception.stacktrace are absent on purpose. The SDK
// writes both itself, from RecordError and from a panic it records in End,
// and both are error text built from whatever the error quoted.
//
// Configuration can remove keys (Config.DenyAttributeKeys) and can never add
// one: adding a key is a reviewed change to this list.
var allowedAttributeKeys = []string{
	// Set by internal/api's TracingMiddleware.
	"http.request.method",
	"http.response.status_code",
	"http.route",
	"network.protocol.name",
	"url.path",

	// Set by internal/runner's handleMessage.
	"messaging.destination.name",
	"pleiades.device.name",
	"pleiades.dispatch.duplicate",
	"pleiades.job.id",
	"pleiades.runbook.id",

	// Set by buildResource.
	"service.name",
	"service.version",

	// Added to the resource by the SDK's resource.Default.
	"telemetry.sdk.language",
	"telemetry.sdk.name",
	"telemetry.sdk.version",

	// Resource identifiers an operator commonly sets through
	// OTEL_RESOURCE_ATTRIBUTES. Each names where a process runs, never what
	// it handles, and each value is still masked on its way out. Any other
	// key that variable sets is dropped.
	"deployment.environment.name",
	"service.instance.id",
	"service.namespace",

	// Added by the SDK to its own exception event. The type is an
	// identifier (a Go type name); the message beside it is not, and is not
	// listed.
	"exception.type",

	// Set by RecordFailure: the fixed category of a failure.
	"error.type",
}

// allowedEventNames are the span event names that may leave this process.
// An event's name is free text chosen by whoever called AddEvent, so an
// event with any other name is dropped whole and counted in the span's
// DroppedEvents. The SDK's own exception event is the one this platform
// relies on, and only its type survives the attribute policy.
var allowedEventNames = []string{"exception"}

// APIScope is the instrumentation scope, the tracer name, of the
// Controller's API request spans. A composition root takes its tracer under
// this name so the exporter recognizes the spans it names.
const APIScope = "github.com/Subject-Void-LLC/the-pleiades/internal/api"

// RunnerScope is the instrumentation scope of the Runner's dispatch spans.
const RunnerScope = "github.com/Subject-Void-LLC/the-pleiades/internal/runner"

// otelHTTPScope is the scope otelhttp's spans carry: the Docker client
// instruments its transport with it. Its name and version are identifiers;
// its span names (the Docker client names them after the request path) are
// not, and are replaced.
const otelHTTPScope = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

// undeclaredScope replaces the name of a scope that is not declared. A
// scope's name is chosen by whoever asked for the tracer, so it is free
// text like any other.
const undeclaredScope = "undeclared"

// declaredScopes are the instrumentation scopes whose name and version may
// be exported.
var declaredScopes = map[string]bool{APIScope: true, RunnerScope: true, otelHTTPScope: true}

// declaredSpanNames are the span names exported as they are, whatever their
// scope.
var declaredSpanNames = map[string]bool{"runner.handle_message": true}

// apiSpanName is the shape of the names internal/api's TracingMiddleware
// gives a request span: a bounded method alone (before routing), or the
// bounded method and either "unmatched" or a chi route pattern. It is
// accepted only for spans of APIScope, and internal/archtest holds that
// middleware to building the name from boundedMethod and routePattern, so a
// raw path in this shape never comes from it.
var apiSpanName = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|CONNECT|OPTIONS|TRACE|_OTHER)( (unmatched|/[A-Za-z0-9._~{}/*-]{0,255}))?$`)

// scopeVersion is the shape of an instrumentation version that may be
// exported: a version number, nothing else.
var scopeVersion = regexp.MustCompile(`^v?[0-9]{1,6}(\.[0-9]{1,6}){0,3}([-+][0-9A-Za-z.]{1,32})?$`)

// errorTypeKey is the attribute RecordFailure writes. Its value is held to
// the declared categories at export, because any instrumentation can write
// the key.
const errorTypeKey = "error.type"

// DenyAttributesEnv names the environment variable ConfigFromEnv reads
// Config.DenyAttributeKeys from: a comma-separated list of allowlisted keys
// this process must not export.
const DenyAttributesEnv = "PLEIADES_TELEMETRY_DENY_ATTRIBUTES"

// withheldDescription replaces a span status description that is not one of
// the fixed phrases. It is a phrase rather than an empty string so an
// operator reading a trace can tell "a description was removed" apart from
// "none was given", and knows to look at the masked log line, which carries
// the same trace_id, for the text.
const withheldDescription = "failure description withheld by telemetry policy"

// AllowedAttributeKeys returns a copy of the fixed allowlist. It is exported
// so internal/archtest checks every call site against the same list the
// exporter enforces, rather than against a second copy that could drift.
func AllowedAttributeKeys() []string {
	return slices.Clone(allowedAttributeKeys)
}

// TelemetryOnlyAttributeKeys returns the allowlisted keys that only this
// package may write: error.type, which RecordFailure sets from a declared
// Outcome, and exception.type, which the SDK sets itself. internal/archtest
// refuses either key anywhere else, so a value under them comes from a
// fixed set rather than from a caller.
func TelemetryOnlyAttributeKeys() []string {
	return []string{errorTypeKey, "exception.type"}
}

// AllowedEventNames returns a copy of the span event names that may be
// exported, for internal/archtest's check of AddEvent call sites.
func AllowedEventNames() []string {
	return slices.Clone(allowedEventNames)
}

// attributePolicy is the allowlist as one process applies it: the fixed keys
// minus whatever that process's configuration denied, the fixed event
// names, and the closed set of status descriptions.
type attributePolicy struct {
	// keys are the attribute keys that may be exported.
	keys map[string]struct{}

	// events are the event names that may be exported.
	events map[string]struct{}

	// descriptions are the status descriptions that may be exported.
	descriptions map[string]struct{}

	// categories are the error.type values that may be exported.
	categories map[string]struct{}
}

// newAttributePolicy builds the policy for one process. deny narrows the
// fixed list, and nothing widens it: there is no parameter that adds a key.
//
// A denied key that is not on the list is an error rather than a no-op, for
// the reason internal/redact refuses an unknown field in its ruleset: a typo
// in a security control must not parse clean and do nothing.
func newAttributePolicy(deny []string) (*attributePolicy, error) {
	p := &attributePolicy{
		keys:         make(map[string]struct{}, len(allowedAttributeKeys)),
		events:       make(map[string]struct{}, len(allowedEventNames)),
		descriptions: make(map[string]struct{}),
		categories:   make(map[string]struct{}),
	}
	for _, k := range allowedAttributeKeys {
		p.keys[k] = struct{}{}
	}
	for _, k := range deny {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !slices.Contains(allowedAttributeKeys, k) {
			return nil, fmt.Errorf("telemetry: denied attribute key %q is not on the allowlist, so denying it would change nothing", k)
		}
		delete(p.keys, k)
	}
	for _, name := range allowedEventNames {
		p.events[name] = struct{}{}
	}

	for _, o := range declaredOutcomes() {
		p.descriptions[o.phrase] = struct{}{}
		p.categories[o.category] = struct{}{}
	}
	// internal/api's TracingMiddleware describes a 5xx with the standard
	// library's own phrase for the code, which is a closed set too.
	for code := 500; code <= 599; code++ {
		if text := http.StatusText(code); text != "" {
			p.descriptions[text] = struct{}{}
		}
	}
	return p, nil
}

// allowsKey reports whether an attribute with this key may be exported.
func (p *attributePolicy) allowsKey(k attribute.Key) bool {
	_, ok := p.keys[string(k)]
	return ok
}

// allowsEvent reports whether an event with this name may be exported.
func (p *attributePolicy) allowsEvent(name string) bool {
	_, ok := p.events[name]
	return ok
}

// description returns the status description to export in place of d: d
// itself when it is empty or one of the fixed phrases, and the withheld
// phrase otherwise. Masking d instead would not be enough, because an error
// that quotes device output has no secret shape for a mask to match.
func (p *attributePolicy) description(d string) string {
	if d == "" {
		return d
	}
	if _, ok := p.descriptions[d]; ok {
		return d
	}
	return withheldDescription
}

// errorType returns the error.type value to export in place of v: v when it
// is a declared category, and the unclassified category otherwise.
func (p *attributePolicy) errorType(v string) string {
	if _, ok := p.categories[v]; ok {
		return v
	}
	return outcomeUnclassified.category
}

// scopeIdentity returns the instrumentation scope name and version to
// export: the name and a version-shaped version only when the scope is
// declared, and the undeclared placeholder with no version otherwise. The
// caller drops the schema URL and filters the scope's attributes.
func scopeIdentity(name, version string) (string, string) {
	if !declaredScopes[name] {
		return undeclaredScope, ""
	}
	if !scopeVersion.MatchString(version) {
		version = ""
	}
	return name, version
}

// spanName returns the span name to export: a declared name, or an
// APIScope span's name in the shape the API builds, as it is; anything else
// becomes the exported scope name plus " span", so a span stays countable
// by where it came from while its own text is withheld.
func spanName(scope, name string) string {
	if declaredSpanNames[name] {
		return name
	}
	if scope == APIScope && apiSpanName.MatchString(name) {
		return name
	}
	return scope + " span"
}
