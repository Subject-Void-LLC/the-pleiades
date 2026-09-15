// The permission sets a minted user carries.
//
// Everything in this file is derived from a real enumeration of what each
// process actually does on the wire, taken during Phase 101b's recon, not
// from what the architecture diagram suggests it should do. That
// distinction is the whole risk here: a permission set is a deny-by-
// default list, so anything the enumeration missed is a process that
// authenticates successfully and then fails at the first operation nobody
// wrote down. Several entries below are things no design document would
// have predicted.

package meshid

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/jwt/v2"
)

// Grant is one principal's permission set, as subject patterns.
//
// It is a plain struct of strings rather than a jwt.UserPermissionLimits
// so it can be compared, logged and tested without the JWT library, and so
// the one function that renders it into claims is the only place that
// knows the library's shape.
type Grant struct {
	// Name labels the minted user. NATS identifies a user by public key,
	// so this is for humans reading server logs.
	Name string

	// Pub and Sub are subject patterns this principal may publish to and
	// subscribe to.
	Pub []string
	Sub []string
}

// inboxPattern is the reply-inbox space every NATS client needs to
// subscribe to in order to receive ANY request/reply response.
//
// This is the entry most likely to be left out, and leaving it out breaks
// everything while looking like it should break nothing. The Runner
// subscribes to no pleiades.* subject at all: it pulls dispatch by
// PUBLISHING a request to $JS.API.CONSUMER.MSG.NEXT and receiving the
// messages on an inbox. A grant that lists every pleiades subject
// perfectly and omits this one produces a Runner that connects, publishes
// happily, and never receives a single job.
const inboxPattern = "_INBOX.>"

// jetStreamAPIInfo is the account-info call nats.go makes unconditionally
// when preparing a key-value bucket, whether or not the caller asked for
// anything that needs it.
const jetStreamAPIInfo = "$JS.API.INFO"

// FleetRunnerGrant returns what a Runner needs to do its whole job against
// the shared fleet consumer.
//
// The stream and bucket CREATE and UPDATE entries are the uncomfortable
// ones and they are deliberate. Both binaries create a missing stream or
// bucket rather than failing, which internal/topology documents as a
// design decision rather than a compromise, so a grant that withholds
// those rights produces a Runner that dies at startup on a healthy mesh
// the first time it races the Controller to provisioning. Narrowing this
// is a change to that startup behaviour first and to this list second.
func FleetRunnerGrant(name string) Grant {
	return Grant{
		Name: name,
		Pub: []string{
			// Application subjects the Runner produces.
			topology.LogSubjectAll(),
			topology.ResultSubjectAll(),
			// The run journal (Phase 40). Withholding this does not
			// produce a permissions error a reader could act on: a denied
			// publish gets no reply at all, so it surfaces as a context
			// deadline inside the sink, which the engine then logs and
			// counts as a failed journal write while the run itself
			// succeeds. That is exactly the shape that looks like "the
			// journal is broken" rather than "the Runner is not allowed".
			topology.JournalSubjectAll(),
			topology.DeadLetterSubject(topology.DispatchSubjectAll()),

			// The per-device execution lease, written both through the KV
			// API and, for the TTL refresh, as a raw publish to the
			// bucket's own subject space.
			kvSubjectSpace(topology.LockBucketName),
			kvSubjectSpace(topology.DedupBucketName),

			// JetStream control plane.
			jetStreamAPIInfo,
			streamAPI("INFO", topology.StreamName),
			streamAPI("CREATE", topology.StreamName),
			streamAPI("UPDATE", topology.StreamName),
			streamAPI("INFO", kvStreamName(topology.LockBucketName)),
			streamAPI("CREATE", kvStreamName(topology.LockBucketName)),
			streamAPI("UPDATE", kvStreamName(topology.LockBucketName)),
			streamAPI("INFO", kvStreamName(topology.DedupBucketName)),
			streamAPI("CREATE", kvStreamName(topology.DedupBucketName)),
			streamAPI("UPDATE", kvStreamName(topology.DedupBucketName)),

			// Reading a KV entry on a bucket created with AllowDirect goes
			// through DIRECT.GET rather than STREAM.MSG.GET, which is not
			// obvious from any call site in this repository: it is decided
			// inside nats.go by the bucket's own config.
			directGetAPI(kvStreamName(topology.LockBucketName)),
			directGetAPI(kvStreamName(topology.DedupBucketName)),

			// The dispatch consumer: created by the Runner itself, probed
			// every ten seconds by the heartbeat, and pulled from.
			consumerCreateWithFilter(topology.StreamName, topology.DispatchDurableName, topology.DispatchSubjectAll()),
			consumerAPI("INFO", topology.StreamName, topology.DispatchDurableName),
			consumerAPI("MSG.NEXT", topology.StreamName, topology.DispatchDurableName),

			// Message settlement. Ack, Nak and Term are all core publishes
			// to the reply subject JetStream stamped on the delivery.
			ackSpace(topology.StreamName, topology.DispatchDurableName),
		},
		Sub: []string{
			inboxPattern,

			// The per-job cancel signal, and the only pleiades subject a
			// Runner subscribes to at all. It has to be the whole control
			// space rather than one job: a grant is minted long before
			// this Runner knows which job it will be given, and the
			// subscription it actually opens names exactly one job.
			//
			// Withholding this is silent, which is why it is here rather
			// than deferred. A denied core subscription produces no error
			// the Runner can act on, so cancellation would simply never
			// reach a running execution while every log line and every
			// test still said the feature worked. Reading the whole space
			// discloses nothing worth withholding: a control message
			// carries a job id and nothing else, deliberately, for exactly
			// this reason.
			topology.ControlSubjectAll(),
		},
	}
}

// ControllerGrant returns what the Controller needs.
//
// It is a superset of the Runner's in the places that matter and a
// different shape in one: the Controller is the only process permitted to
// RESHAPE a live stream, which internal/topology models as the
// StreamProvisioner role, and it serves per-viewer log consumers whose
// names the server generates, so it cannot be scoped to a fixed consumer
// name the way the Runner can.
func ControllerGrant(name string) Grant {
	return Grant{
		Name: name,
		Pub: []string{
			topology.DispatchSubjectAll(),
			topology.JobRequestedSubject(),
			topology.EventSubject(">"),

			// The per-job cancel signal the Runner grant above subscribes
			// to. Core NATS, so a denied publish IS reported here, unlike
			// the denied subscribe on the other side, which is not. The
			// two halves of a cancel therefore fail independently: a job
			// can settle to canceled with nothing having stopped.
			topology.ControlSubjectAll(),
			kvSubjectSpace(topology.LockBucketName),

			// The Controller's own dead letter path. Its absence was
			// silent by construction: internal/event/dlq.go returns
			// before msg.Term() when the publish fails, so a five times
			// failed job.requested was neither dead-lettered nor
			// terminated, and the job simply vanished. A DLQ that cannot
			// publish is the one component whose failure nothing else
			// reports.
			topology.DeadLetterSubject(topology.JobRequestedSubject()),

			// And the run journal consumer's, for the identical reason
			// and by the identical mechanism (Phase 40). A handler error
			// routes into event.HandleDeliveryFailure, which publishes to
			// the dead letter subject and returns BEFORE msg.Term() if
			// that publish fails, so a journal batch the store keeps
			// refusing would be neither dead-lettered nor terminated and
			// would simply cycle. The subject is derived from the message
			// the consumer received, so it is the wildcard over every
			// job's journal, not one job's.
			topology.DeadLetterSubject(topology.JournalSubjectAll()),

			jetStreamAPIInfo,
			// Named operations rather than a wildcard. `streamAPI(">")`
			// put `>` in a NON-FINAL token
			// ("$JS.API.STREAM.>.PLEIADES"), which is not a wildcard
			// position at all, so the Controller had no usable stream
			// permission and died at startup in ProvisionStream. Measured
			// against a real broker: the request is denied, no reply
			// comes back, and the failure surfaces as a context deadline
			// rather than as a permissions error.
			streamAPI("INFO", topology.StreamName),
			streamAPI("CREATE", topology.StreamName),
			streamAPI("UPDATE", topology.StreamName),
			streamAPI("INFO", kvStreamName(topology.LockBucketName)),
			streamAPI("CREATE", kvStreamName(topology.LockBucketName)),
			streamAPI("UPDATE", kvStreamName(topology.LockBucketName)),
			directGetAPI(kvStreamName(topology.LockBucketName)),

			// Server-named ephemeral consumers for the SSE log viewer, so
			// the consumer token cannot be pinned here.
			fmt.Sprintf("$JS.API.CONSUMER.CREATE.%s.>", topology.StreamName),
			fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.>", topology.StreamName),
			fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.>", topology.StreamName),
			fmt.Sprintf("$JS.API.CONSUMER.DELETE.%s.>", topology.StreamName),
			fmt.Sprintf("$JS.ACK.%s.>", topology.StreamName),
		},
		Sub: []string{inboxPattern},
	}
}

// kvStreamName is the stream a KV bucket is stored as. NATS derives it by
// prefixing the bucket name, and a permission grant has to name the stream
// rather than the bucket, which is the sort of detail that is invisible
// until a grant silently fails.
func kvStreamName(bucket string) string { return "KV_" + bucket }

// kvSubjectSpace is the subject space a KV bucket's entries live on.
func kvSubjectSpace(bucket string) string { return fmt.Sprintf("$KV.%s.>", bucket) }

func streamAPI(op, stream string) string {
	return fmt.Sprintf("$JS.API.STREAM.%s.%s", op, stream)
}

func directGetAPI(stream string) string {
	return fmt.Sprintf("$JS.API.DIRECT.GET.%s.>", stream)
}

// consumerAPI is the EXACT API subject for an operation on one named
// consumer, with no trailing wildcard.
//
// The wildcard this used to end in was a defect, and an instructive one:
// `>` matches ONE OR MORE trailing tokens and never zero, which Phase 101a
// verified against a real broker and wrote down, and nats.go's templates
// for these two operations end at the consumer name
// (`apiConsumerInfoT = "CONSUMER.INFO.%s.%s"`,
// `apiRequestNextT = "CONSUMER.MSG.NEXT.%s.%s"`, jetstream/api.go:58,61).
// A grant ending in `.>` therefore covered every subject EXCEPT the one
// the driver actually sends. The consequence was not a visible error: a
// denied JetStream request gets no reply, so the Runner's fetch and its
// ten-second heartbeat probe both simply timed out, and the Runner
// authenticated, created its consumer and never received a job.
func consumerAPI(op, stream, consumer string) string {
	return fmt.Sprintf("$JS.API.CONSUMER.%s.%s.%s", op, stream, consumer)
}

// consumerCreateWithFilter is CONSUMER.CREATE for a consumer carrying a
// filter subject, which nats.go appends to the API subject as further
// tokens (`apiConsumerCreateWithFilterSubjectT`).
//
// Naming the filter here rather than ending at the consumer name is the
// one place a Runner's reach is actually narrowed by this grant. The
// Runner creates its own consumer rather than binding one, and
// CreateOrUpdateConsumer is an UPSERT, so a Runner permitted to create
// with any filter could reshape the single shared durable to
// `pleiades.>` and read every other device's dispatch payload, which
// carries resolved plaintext credentials. Pinning the filter prefix means
// the only consumer it can assert is the fleet one it is supposed to
// join.
func consumerCreateWithFilter(stream, consumer, filter string) string {
	return fmt.Sprintf("$JS.API.CONSUMER.CREATE.%s.%s.%s", stream, consumer, filter)
}

func ackSpace(stream, consumer string) string {
	return fmt.Sprintf("$JS.ACK.%s.%s.>", stream, consumer)
}

// apply writes g onto a user's claims.
func (g Grant) apply(c *jwt.UserClaims) {
	c.Name = g.Name
	c.Permissions.Pub.Allow = append(jwt.StringList{}, g.Pub...)
	c.Permissions.Sub.Allow = append(jwt.StringList{}, g.Sub...)
}
