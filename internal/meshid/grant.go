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
			consumerAPI("CREATE", topology.StreamName, topology.DispatchDurableName),
			consumerAPI("INFO", topology.StreamName, topology.DispatchDurableName),
			consumerAPI("MSG.NEXT", topology.StreamName, topology.DispatchDurableName),

			// Message settlement. Ack, Nak and Term are all core publishes
			// to the reply subject JetStream stamped on the delivery.
			ackSpace(topology.StreamName, topology.DispatchDurableName),
		},
		Sub: []string{inboxPattern},
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
			kvSubjectSpace(topology.LockBucketName),

			jetStreamAPIInfo,
			streamAPI(">", topology.StreamName),
			streamAPI(">", kvStreamName(topology.LockBucketName)),
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

func consumerAPI(op, stream, consumer string) string {
	// CONSUMER.CREATE carries the filter subject as further tokens when
	// the consumer is created with one, so this ends in a wildcard rather
	// than at the consumer name. That is also the seam a scoped grant
	// eventually narrows: which FILTER a principal may create a consumer
	// with is expressible here, and it is the only thing that can scope
	// what a pull consumer receives.
	return fmt.Sprintf("$JS.API.CONSUMER.%s.%s.%s.>", op, stream, consumer)
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
