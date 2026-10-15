// The operation table: what each operation a caller may declare is allowed
// to send, and how much its answer may hold.
package containerengine

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Op is one engine API operation a caller may make. A caller declares the
// operations it needs when it opens a Session, and every request outside
// them is refused on the wire before a byte reaches the engine (guard.go).
// That is Phase 73's exec-only rule generalized: container.exec can still
// only exec, whatever the client in its hands could otherwise send.
type Op string

// The operations. Each names one route, or a few that are one operation
// (an image pull and an import share POST /images/create, told apart by
// their query).
const (
	OpInfo     Op = "info"
	OpVersion  Op = "version"
	OpSystemDF Op = "system.df"

	OpContainerList        Op = "container.list"
	OpContainerInspect     Op = "container.inspect"
	OpContainerCreate      Op = "container.create"
	OpContainerStart       Op = "container.start"
	OpContainerStop        Op = "container.stop"
	OpContainerKill        Op = "container.kill"
	OpContainerRestart     Op = "container.restart"
	OpContainerRemove      Op = "container.remove"
	OpContainerWait        Op = "container.wait"
	OpContainerLogs        Op = "container.logs"
	OpContainerAttach      Op = "container.attach"
	OpContainerArchiveRead Op = "container.archive.read"
	OpContainerArchivePut  Op = "container.archive.put"
	OpContainerExport      Op = "container.export"
	OpContainerRename      Op = "container.rename"
	OpContainerUpdate      Op = "container.update"
	OpContainerPrune       Op = "container.prune"

	OpExecCreate  Op = "exec.create"
	OpExecStart   Op = "exec.start"
	OpExecInspect Op = "exec.inspect"

	OpImageList    Op = "image.list"
	OpImageInspect Op = "image.inspect"
	OpImagePull    Op = "image.pull"
	OpImageImport  Op = "image.import"
	OpImagePush    Op = "image.push"
	OpImageTag     Op = "image.tag"
	OpImageRemove  Op = "image.remove"
	OpImageLoad    Op = "image.load"
	OpImageSave    Op = "image.save"
	OpImageSearch  Op = "image.search"
	OpImageHistory Op = "image.history"
	OpImagePrune   Op = "image.prune"
	OpImageBuild   Op = "image.build"
	OpBuildPrune   Op = "build.prune"

	OpNetworkList       Op = "network.list"
	OpNetworkInspect    Op = "network.inspect"
	OpNetworkCreate     Op = "network.create"
	OpNetworkRemove     Op = "network.remove"
	OpNetworkConnect    Op = "network.connect"
	OpNetworkDisconnect Op = "network.disconnect"
	OpNetworkPrune      Op = "network.prune"

	OpVolumeList    Op = "volume.list"
	OpVolumeInspect Op = "volume.inspect"
	OpVolumeCreate  Op = "volume.create"
	OpVolumeRemove  Op = "volume.remove"
	OpVolumePrune   Op = "volume.prune"

	OpRegistryAuth Op = "registry.auth"

	// OpDistributionInspect asks the registry, through the engine, what a
	// reference names now: its descriptor's digest and platforms.
	OpDistributionInspect Op = "distribution.inspect"

	// Podman's own API, for what has no Docker-compatible form.
	OpLibpodInfo Op = "libpod.info"
)

// route is one method and path an operation may send. The path is matched
// with the API version prefix removed (/v1.44, /v5.8.7), and query names
// the query keys that must be present, or must be absent when prefixed
// with "!".
type route struct {
	method string
	path   *regexp.Regexp
	query  []string
}

// segment matches one path segment an identifier or name occupies: never a
// slash, so an identifier cannot reshape the path.
const segment = `[^/]+`

// r builds a route.
func r(method, path string, query ...string) route {
	return route{method: method, path: regexp.MustCompile(`^` + path + `$`), query: query}
}

// routes is the one table of what each operation may send.
var routes = map[Op][]route{
	OpInfo:     {r("GET", `/info`)},
	OpVersion:  {r("GET", `/version`)},
	OpSystemDF: {r("GET", `/system/df`)},

	OpContainerList:        {r("GET", `/containers/json`)},
	OpContainerInspect:     {r("GET", `/containers/`+segment+`/json`)},
	OpContainerCreate:      {r("POST", `/containers/create`)},
	OpContainerStart:       {r("POST", `/containers/`+segment+`/start`)},
	OpContainerStop:        {r("POST", `/containers/`+segment+`/stop`)},
	OpContainerKill:        {r("POST", `/containers/`+segment+`/kill`)},
	OpContainerRestart:     {r("POST", `/containers/`+segment+`/restart`)},
	OpContainerRemove:      {r("DELETE", `/containers/`+segment)},
	OpContainerWait:        {r("POST", `/containers/`+segment+`/wait`)},
	OpContainerLogs:        {r("GET", `/containers/`+segment+`/logs`)},
	OpContainerAttach:      {r("POST", `/containers/`+segment+`/attach`)},
	OpContainerArchiveRead: {r("GET", `/containers/`+segment+`/archive`), r("HEAD", `/containers/`+segment+`/archive`)},
	OpContainerArchivePut:  {r("PUT", `/containers/`+segment+`/archive`)},
	OpContainerExport:      {r("GET", `/containers/`+segment+`/export`)},
	OpContainerRename:      {r("POST", `/containers/`+segment+`/rename`)},
	OpContainerUpdate:      {r("POST", `/containers/`+segment+`/update`)},
	OpContainerPrune:       {r("POST", `/containers/prune`)},

	OpExecCreate:  {r("POST", `/containers/`+segment+`/exec`)},
	OpExecStart:   {r("POST", `/exec/`+segment+`/start`)},
	OpExecInspect: {r("GET", `/exec/`+segment+`/json`)},

	OpImageList:    {r("GET", `/images/json`)},
	OpImageInspect: {r("GET", `/images/.+/json`)},
	OpImagePull:    {r("POST", `/images/create`, "fromImage", "!fromSrc")},
	OpImageImport:  {r("POST", `/images/create`, "fromSrc", "!fromImage")},
	OpImagePush:    {r("POST", `/images/.+/push`)},
	OpImageTag:     {r("POST", `/images/.+/tag`)},
	OpImageRemove:  {r("DELETE", `/images/.+`)},
	OpImageLoad:    {r("POST", `/images/load`)},
	OpImageSave:    {r("GET", `/images/get`), r("GET", `/images/.+/get`)},
	OpImageSearch:  {r("GET", `/images/search`)},
	OpImageHistory: {r("GET", `/images/.+/history`)},
	OpImagePrune:   {r("POST", `/images/prune`)},
	OpImageBuild:   {r("POST", `/build`)},
	OpBuildPrune:   {r("POST", `/build/prune`)},

	OpNetworkList:       {r("GET", `/networks`)},
	OpNetworkInspect:    {r("GET", `/networks/`+segment)},
	OpNetworkCreate:     {r("POST", `/networks/create`)},
	OpNetworkRemove:     {r("DELETE", `/networks/`+segment)},
	OpNetworkConnect:    {r("POST", `/networks/`+segment+`/connect`)},
	OpNetworkDisconnect: {r("POST", `/networks/`+segment+`/disconnect`)},
	OpNetworkPrune:      {r("POST", `/networks/prune`)},

	OpVolumeList:    {r("GET", `/volumes`)},
	OpVolumeInspect: {r("GET", `/volumes/`+segment)},
	OpVolumeCreate:  {r("POST", `/volumes/create`)},
	OpVolumeRemove:  {r("DELETE", `/volumes/`+segment)},
	OpVolumePrune:   {r("POST", `/volumes/prune`)},

	OpRegistryAuth: {r("POST", `/auth`)},

	OpDistributionInspect: {r("GET", `/distribution/.+/json`)},

	OpLibpodInfo: {r("GET", `/libpod/info`)},
}

// alwaysAllowed are requests every session may send: the version
// negotiation the client makes before its first call, which reads nothing
// and changes nothing.
var alwaysAllowed = []route{r("GET", `/_ping`), r("HEAD", `/_ping`)}

// apiVersionPrefix is the version segment both engines accept at the front
// of a path: Docker's /v1.44, Podman's /v5.8.7.
var apiVersionPrefix = regexp.MustCompile(`^/v[0-9]+(\.[0-9]+){1,2}/`)

// KnownOp reports whether op is in the table, so a caller's declaration is
// checked when its session opens rather than refusing everything later.
func KnownOp(op Op) bool {
	_, ok := routes[op]
	return ok
}

// allowed checks one request line's method and request target against ops.
// It is the one function every request a session sends passes through.
//
// It decides on the target exactly as written, and refuses one that is not
// in canonical form rather than decoding it, so the string judged here is the
// string the engine parses: an encoded slash, a dot segment, a character
// that is not valid in a path, or an escaping other than the canonical one
// are each refused. Docker's own client sends only canonical targets, so
// nothing it sends is refused for its form. (Found by FuzzGuard_RequestLine:
// a target holding an invalid character made the URL parser drop its raw
// form, so the guard judged a decoded path the engine never received.)
func allowed(ops []Op, method, target string) (Op, error) {
	path, rawQuery, _ := strings.Cut(target, "?")
	if err := canonicalPath(path); err != nil {
		return "", fmt.Errorf("containerengine: refusing request target %q: %w", target, err)
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("containerengine: refusing request target %q: its query does not parse: %w", target, err)
	}
	if loc := apiVersionPrefix.FindStringIndex(path); loc != nil {
		path = path[loc[1]-1:]
	}
	for _, rt := range alwaysAllowed {
		if rt.matches(method, path, query) {
			return opPing, nil
		}
	}
	for _, op := range ops {
		for _, rt := range routes[op] {
			if rt.matches(method, path, query) {
				return op, nil
			}
		}
	}
	return "", fmt.Errorf("containerengine: refusing %s %s: not an operation this caller declared", method, path)
}

// opPing names the version negotiation every session may send, for the
// response bound; it is not an operation a caller declares.
const opPing Op = "ping"

// The most an engine's answer may hold, by what the answer is. A connection
// carries one request (keep-alive is off), so the bound is the whole answer,
// headers included.
const (
	// maxPingAnswer bounds a ping's answer, which is headers and "OK".
	maxPingAnswer = 64 << 10
	// maxDocumentAnswer bounds a JSON document: an inspection, a list, the
	// engine's info. 64 MiB is several times the largest listing a busy host
	// produces, and far below what would hurt the process reading it.
	maxDocumentAnswer = 64 << 20
	// maxProgressAnswer bounds a progress stream (pull, push, load, import,
	// build), one small JSON message per step: long for a large image, never
	// unbounded.
	maxProgressAnswer = 256 << 20
	// unbounded marks a stream whose size is the caller's to bound, because
	// it is the content itself (logs, an attached stream, an archive, an
	// image or filesystem export): the method reading it decides how much
	// it keeps, as Exec does with ExecOptions.MaxOutputBytes.
	unbounded = 0
)

// answerBound is the most each operation's answer may hold; an operation
// not named here answers a document.
var answerBound = map[Op]int64{
	opPing:                 maxPingAnswer,
	OpImagePull:            maxProgressAnswer,
	OpImagePush:            maxProgressAnswer,
	OpImageLoad:            maxProgressAnswer,
	OpImageImport:          maxProgressAnswer,
	OpImageBuild:           maxProgressAnswer,
	OpContainerLogs:        unbounded,
	OpContainerAttach:      unbounded,
	OpContainerArchiveRead: unbounded,
	OpContainerExport:      unbounded,
	OpExecStart:            unbounded,
	OpImageSave:            unbounded,
}

// boundFor returns op's answer bound, unbounded meaning the caller bounds it.
func boundFor(op Op) int64 {
	if b, ok := answerBound[op]; ok {
		return b
	}
	return maxDocumentAnswer
}

// canonicalPath refuses a request path that is not exactly its own
// canonical escaping, or that holds a form an engine could read as a
// different path: an encoded slash, or a "." or ".." segment.
func canonicalPath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("it is not a path")
	}
	if strings.Contains(strings.ToLower(path), "%2f") {
		return fmt.Errorf("it holds an encoded slash")
	}
	// Canonical form never encodes an unreserved character; an encoded dot
	// could otherwise hide a ".." segment from the check below.
	for i := 0; i+2 < len(path); i++ {
		if path[i] != '%' {
			continue
		}
		b, err := strconv.ParseUint(path[i+1:i+3], 16, 8)
		if err != nil {
			return fmt.Errorf("it holds a malformed escape")
		}
		if c := byte(b); c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			return fmt.Errorf("it encodes %q, which canonical form writes as itself", c)
		}
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("it holds a dot segment")
		}
	}
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return err
	}
	if u.EscapedPath() != path {
		return fmt.Errorf("it is not in canonical form")
	}
	return nil
}

// matches reports whether a request is this route.
func (rt route) matches(method, path string, query url.Values) bool {
	if method != rt.method || !rt.path.MatchString(path) {
		return false
	}
	for _, q := range rt.query {
		if absent, ok := strings.CutPrefix(q, "!"); ok {
			if query.Has(absent) {
				return false
			}
			continue
		}
		if !query.Has(q) {
			return false
		}
	}
	return true
}
