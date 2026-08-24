package netconf

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// GetConfig reads the configuration subtree p addresses from this
// session's target datastore, or the whole datastore when p is the zero
// Path.
//
// The returned Payload carries the raw XML inside the reply's <data>
// element, verbatim and unparsed. Handing back bytes rather than a
// decoded tree is deliberate: this client has no YANG schema, so any
// structure it imposed would be its own invention, and a caller that
// wants structure has pkg/filters' XMLToJSON to apply to it.
func (s *Session) GetConfig(ctx context.Context, p datastore.Path) (datastore.Payload, error) {
	filter, err := subtreeFilter(p)
	if err != nil {
		return datastore.Payload{}, err
	}

	body := fmt.Sprintf("<get-config><source><%s/></source>%s</get-config>", s.opts.target(), filter)
	reply, err := s.do(ctx, body)
	if err != nil {
		return datastore.Payload{}, err
	}
	if reply.Data == nil {
		// A reply with neither <data> nor an <rpc-error> is a protocol
		// violation, not an empty configuration: an empty subtree comes
		// back as an empty <data/>, which decodes to a non-nil Data with
		// no inner XML.
		return datastore.Payload{}, fmt.Errorf("netconf: the server's reply to get-config %s carried no <data> element", p)
	}
	return datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte(reply.Data.Inner)}, nil
}

// SetConfig applies payload to the subtree p addresses.
//
// # rollback-on-error is requested whenever the device offers it
//
// RFC 6241's default error handling is stop-on-error, which leaves a
// rejected batch HALF APPLIED: the lines before the failure stay, the
// rest do not, and the device is in a state neither the runbook nor the
// operator described. When the server advertises :rollback-on-error this
// client requests it unconditionally, making the edit all-or-nothing.
// There is deliberately no option to turn that off: a partially applied
// configuration change is not a behavior anyone asks for on purpose, and
// a knob for it would only ever be found by someone who had already been
// bitten. A real Cisco IOS XE device does advertise it.
//
// This matters more here than it would on a device with a candidate
// datastore, because a device offering only :writable-running (which is
// exactly what Cisco IOS XE offers) has no staging area: every edit lands
// on the live configuration as it is applied.
func (s *Session) SetConfig(ctx context.Context, p datastore.Path, payload datastore.Payload, op datastore.Operation) error {
	if !op.Valid() {
		return fmt.Errorf("netconf: unknown operation %s", op)
	}
	if op == datastore.OperationDelete {
		if !payload.IsEmpty() {
			return fmt.Errorf("netconf: a %s carries no payload, but %d bytes were supplied: refusing rather than silently discarding configuration the caller passed", op, len(payload.Bytes))
		}
		if p.IsRoot() {
			return fmt.Errorf("netconf: refusing to %s the root of the %s datastore: name the subtree to remove", op, s.opts.target())
		}
	} else if payload.IsEmpty() {
		return fmt.Errorf("netconf: a %s needs a payload, but none was supplied", op)
	}
	if !payload.IsEmpty() && payload.Encoding != datastore.EncodingXML {
		return fmt.Errorf("netconf: NETCONF carries XML, but the payload is %s encoded", payload.Encoding)
	}

	config, err := wrapInPath(p, string(payload.Bytes), op)
	if err != nil {
		return err
	}

	// A non-root path scopes the operation to the innermost element it
	// names (wrapInPath writes the attribute) and tells the device to do
	// nothing by default. See wrapInPath's own doc comment for why the
	// alternative is dangerous rather than merely different.
	defaultOp := DefaultOperationNone
	if p.IsRoot() {
		defaultOp = rootDefaultOperation(op)
	}

	return s.EditConfig(ctx, EditConfigRequest{Config: config, DefaultOperation: defaultOp})
}

// The three values RFC 6241 section 7.2 defines for edit-config's
// default-operation. Note that "delete" is NOT among them: a delete is
// always expressed by a per-element operation attribute, which is why
// SetConfig refuses a delete addressed at the root rather than trying to
// express one.
const (
	DefaultOperationMerge   = "merge"
	DefaultOperationReplace = "replace"
	DefaultOperationNone    = "none"
)

// The three values RFC 6241 section 7.2 defines for edit-config's
// error-option. Only rollback-on-error requires a capability.
const (
	ErrorOptionStopOnError     = "stop-on-error"
	ErrorOptionContinueOnError = "continue-on-error"
	ErrorOptionRollbackOnError = "rollback-on-error"
)

// EditConfigRequest carries RFC 6241 section 7.2's full edit-config
// vocabulary.
//
// It exists alongside SetConfig rather than instead of it because the
// two answer different questions. SetConfig is the pkg/datastore.Store
// port: the operations NETCONF, RESTCONF and gNMI genuinely share, which
// is why its Operation set has no "none" (RESTCONF and gNMI have no
// concept of it) and no error-option at all. This type is NETCONF's own
// vocabulary in full, reached by a caller that has a *Session in hand,
// the same way Commit and Lock are. net.netconf.config uses this one,
// because an operator writing a runbook against a NETCONF device is
// entitled to the protocol's real vocabulary rather than the
// intersection of three protocols'.
type EditConfigRequest struct {
	// Config is the raw XML that becomes the <config> element's
	// contents. It is checked for well-formedness and nesting depth
	// before anything is written.
	Config string

	// DefaultOperation is what the device does with elements carrying no
	// explicit operation attribute. Empty means merge, RFC 6241's own
	// default.
	DefaultOperation string

	// ErrorOption is how the device handles a rejected element. Empty
	// means rollback-on-error when the server advertises the capability
	// and RFC 6241's own stop-on-error default when it does not; see
	// this method's doc comment for why that automatic choice is not
	// configurable.
	ErrorOption string
}

// EditConfig applies req to this session's target datastore.
//
// # rollback-on-error is requested whenever the device offers it
//
// RFC 6241's default error handling is stop-on-error, which leaves a
// rejected batch HALF APPLIED: the elements before the failure stay, the
// rest do not, and the device is in a state neither the runbook nor the
// operator described. When the server advertises :rollback-on-error and
// the caller expressed no preference, this client requests it, making
// the edit all-or-nothing. A caller that genuinely wants partial
// application can still ask for it by name through ErrorOption; what
// there is no way to get is that behavior by accident.
//
// This matters more here than it would on a device with a candidate
// datastore, because a device offering only :writable-running (which is
// exactly what Cisco IOS XE offers) has no staging area at all: every
// element lands on the live configuration as it is applied.
func (s *Session) EditConfig(ctx context.Context, req EditConfigRequest) error {
	if strings.TrimSpace(req.Config) == "" {
		return fmt.Errorf("netconf: edit-config needs configuration to apply, but none was supplied")
	}
	// Well-formedness and depth are checked HERE, before anything is
	// written, rather than being left for the device to complain about.
	// A malformed payload sent to a device that has already begun
	// applying an edit is the worst version of this failure, and the
	// depth bound in particular is this client's own: nothing in
	// encoding/xml applies one to a subtree carried as raw bytes.
	if err := checkWellFormed([]byte(req.Config), s.opts.maxDepth()); err != nil {
		return fmt.Errorf("netconf: the configuration payload is not usable: %w", err)
	}

	defaultOp := req.DefaultOperation
	if defaultOp == "" {
		defaultOp = DefaultOperationMerge
	}
	switch defaultOp {
	case DefaultOperationMerge, DefaultOperationReplace, DefaultOperationNone:
	default:
		return fmt.Errorf("netconf: unknown default-operation %q: RFC 6241 defines merge, replace and none", defaultOp)
	}

	errorOption := req.ErrorOption
	switch errorOption {
	case "":
		if s.HasCapability(CapabilityRollbackOnError) {
			errorOption = ErrorOptionRollbackOnError
		}
	case ErrorOptionStopOnError, ErrorOptionContinueOnError:
	case ErrorOptionRollbackOnError:
		if !s.HasCapability(CapabilityRollbackOnError) {
			return fmt.Errorf("netconf: this device does not support rollback-on-error (it does not advertise %s)", CapabilityRollbackOnError)
		}
	default:
		return fmt.Errorf("netconf: unknown error-option %q: RFC 6241 defines stop-on-error, continue-on-error and rollback-on-error", errorOption)
	}

	if s.opts.target() == Running && !s.HasCapability(CapabilityWritableRunning) {
		return fmt.Errorf("netconf: this device does not allow writing to the running datastore directly (it does not advertise %s); target the candidate datastore and Commit instead", CapabilityWritableRunning)
	}

	var errorElement string
	if errorOption != "" {
		errorElement = fmt.Sprintf("<error-option>%s</error-option>", errorOption)
	}

	body := fmt.Sprintf(
		"<edit-config><target><%s/></target><default-operation>%s</default-operation>%s<config>%s</config></edit-config>",
		s.opts.target(), defaultOp, errorElement, req.Config)

	reply, err := s.do(ctx, body)
	if err != nil {
		return err
	}
	if reply.OK == nil {
		return fmt.Errorf("netconf: the server's reply to edit-config was neither <ok/> nor an <rpc-error>")
	}
	return nil
}

// rootDefaultOperation maps a port Operation to edit-config's
// default-operation value, and is used ONLY for a root-addressed
// payload, where there are no ancestor wrappers for the default to
// affect unintentionally.
//
// RFC 6241 defines exactly three values here: merge, replace and none.
// There is no "delete", which is why a delete is always expressed by a
// per-element operation attribute, and why SetConfig refuses a delete
// addressed at the root outright rather than trying to express one.
func rootDefaultOperation(op datastore.Operation) string {
	if op == datastore.OperationReplace {
		return DefaultOperationReplace
	}
	return DefaultOperationMerge
}

// Lock acquires a lock on this session's target datastore, so another
// client cannot change it underneath a read-modify-write.
func (s *Session) Lock(ctx context.Context) error {
	return s.expectOK(ctx, fmt.Sprintf("<lock><target><%s/></target></lock>", s.opts.target()), "lock")
}

// Unlock releases the lock Lock acquired.
func (s *Session) Unlock(ctx context.Context) error {
	return s.expectOK(ctx, fmt.Sprintf("<unlock><target><%s/></target></unlock>", s.opts.target()), "unlock")
}

// Commit copies the candidate datastore into running.
//
// It is meaningless without :candidate and is refused on a server that
// does not advertise it, rather than being sent and rejected. This is
// the datastore-family operation the shared pkg/datastore.Store port
// deliberately does not carry: RESTCONF and gNMI have no commit at all,
// so a caller reaches it by asserting for this concrete type. A real
// Cisco IOS XE device does not offer :candidate, so this is exercised
// against the container conformance target rather than against the
// sandbox.
func (s *Session) Commit(ctx context.Context) error {
	if !s.HasCapability(CapabilityCandidate) {
		return fmt.Errorf("netconf: this device has no candidate datastore to commit (it does not advertise %s)", CapabilityCandidate)
	}
	return s.expectOK(ctx, "<commit/>", "commit")
}

// DiscardChanges throws away uncommitted candidate changes.
func (s *Session) DiscardChanges(ctx context.Context) error {
	if !s.HasCapability(CapabilityCandidate) {
		return fmt.Errorf("netconf: this device has no candidate datastore to discard (it does not advertise %s)", CapabilityCandidate)
	}
	return s.expectOK(ctx, "<discard-changes/>", "discard-changes")
}

// Validate asks the server to check the target datastore's contents
// without applying anything.
func (s *Session) Validate(ctx context.Context) error {
	if !s.HasCapability(CapabilityValidate10) {
		return fmt.Errorf("netconf: this device does not support validate (it does not advertise %s)", CapabilityValidate10)
	}
	return s.expectOK(ctx, fmt.Sprintf("<validate><source><%s/></source></validate>", s.opts.target()), "validate")
}

// Close ends the session politely with <close-session/> and then closes
// the underlying stream.
//
// The stream is closed even when the RPC fails, and the RPC's error is
// still returned. Skipping the close on a failed RPC would leak the SSH
// channel for exactly the case where the peer is already misbehaving,
// which is the case least able to clean up after itself.
func (s *Session) Close(ctx context.Context) error {
	rpcErr := s.expectOK(ctx, "<close-session/>", "close-session")
	closeErr := s.rwc.Close()
	if rpcErr != nil {
		return rpcErr
	}
	if closeErr != nil && closeErr != io.EOF {
		return fmt.Errorf("netconf: closing the session stream: %w", closeErr)
	}
	return nil
}

// expectOK issues body and requires an <ok/> reply.
func (s *Session) expectOK(ctx context.Context, body, name string) error {
	reply, err := s.do(ctx, body)
	if err != nil {
		return err
	}
	if reply.OK == nil {
		return fmt.Errorf("netconf: the server's reply to %s was neither <ok/> nor an <rpc-error>", name)
	}
	return nil
}

// do wraps body in an <rpc> envelope, sends it, and reads and validates
// the matching reply.
//
// The message-id is compared, not merely carried. RFC 6241 requires a
// server to echo it, and a reply bearing a different one means this
// client and the device disagree about which request is being answered,
// which is a state no amount of parsing recovers from: accepting it
// would attribute one RPC's result to another. There is no
// resynchronization attempt for the same reason Shell does not attempt
// one after a bad read.
func (s *Session) do(ctx context.Context, body string) (*rpcReply, error) {
	stop := s.watchContext(ctx)
	defer stop()

	id := strconv.FormatInt(s.messageID.Add(1), 10)
	msg := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rpc message-id="%s" xmlns=%q>%s</rpc>`,
		id, baseNamespace, body)

	if err := s.w.writeMessage([]byte(msg)); err != nil {
		return nil, wrapCtx(ctx, err)
	}

	raw, err := s.r.readMessage()
	if err != nil {
		return nil, fmt.Errorf("netconf: reading the reply to message-id %s: %w", id, wrapCtx(ctx, err))
	}
	if err := checkDepth(raw, s.opts.maxDepth()); err != nil {
		return nil, fmt.Errorf("netconf: reply to message-id %s: %w", id, err)
	}

	var reply rpcReply
	if err := xml.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("netconf: parsing the reply to message-id %s: %w", id, err)
	}
	if reply.MessageID != id {
		return nil, fmt.Errorf("netconf: the server answered message-id %q with a reply for message-id %q; this session is no longer synchronized and is not safe to continue using",
			id, reply.MessageID)
	}
	if len(reply.Errors) > 0 {
		errs := make(RPCErrors, 0, len(reply.Errors))
		for _, e := range reply.Errors {
			errs = append(errs, e.decode())
		}
		return nil, errs
	}
	return &reply, nil
}

// rpcReply is the subset of <rpc-reply> this client reads.
//
// Data is a pointer to a struct capturing inner XML rather than a
// string field with the ,innerxml tag on rpcReply itself: that tag on
// the outer element would capture the <rpc-error> elements too, and it
// would give no way to tell an absent <data> from an empty one.
type rpcReply struct {
	XMLName   xml.Name      `xml:"rpc-reply"`
	MessageID string        `xml:"message-id,attr"`
	Errors    []rpcErrorXML `xml:"rpc-error"`
	OK        *okElement    `xml:"ok"`
	Data      *innerElement `xml:"data"`
}

type okElement struct{}

type innerElement struct {
	Inner string `xml:",innerxml"`
}

// rpcErrorXML is the wire shape of one <rpc-error>. Its field set was
// taken from a real Cisco IOS XE device's replies, which is why
// Message is treated as genuinely optional here: that device omits it
// entirely for an unknown-element error.
type rpcErrorXML struct {
	Type     string `xml:"error-type"`
	Tag      string `xml:"error-tag"`
	Severity string `xml:"error-severity"`
	Path     string `xml:"error-path"`
	Message  string `xml:"error-message"`
	Info     *struct {
		BadElement string `xml:"bad-element"`
		Inner      string `xml:",innerxml"`
	} `xml:"error-info"`
}

// decode converts the wire shape to the exported RPCError, trimming the
// whitespace a real device pretty-prints around element text: its
// error-path arrives as "\n    /rpc\n  ", and an untrimmed path would
// render every error message with a line break in the middle of it.
func (e rpcErrorXML) decode() RPCError {
	out := RPCError{
		Type:     strings.TrimSpace(e.Type),
		Tag:      strings.TrimSpace(e.Tag),
		Severity: strings.TrimSpace(e.Severity),
		Path:     strings.TrimSpace(e.Path),
		Message:  strings.TrimSpace(e.Message),
	}
	if e.Info != nil {
		out.BadElement = strings.TrimSpace(e.Info.BadElement)
		out.Info = e.Info.Inner
	}
	return out
}
