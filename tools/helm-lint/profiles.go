// The configurations this tool renders, and the ones it requires to be
// refused.
//
// Split out of main.go so the tables read as data: adding an arrangement the
// chart has to support, or a mistake it has to reject, is an entry here and no
// change to the code that runs them.
package main

// basePostgresPassword is the database password every positive profile
// renders with. It is named because checkFingerprintFormulaAgrees computes
// the chart's credential fingerprint from it on the Go side.
const basePostgresPassword = "helm-lint-render-only" // #nosec G101 -- a literal `helm template` renders with; nothing is installed and no database ever holds it

// The values every positive profile supplies, because the chart deliberately
// refuses to invent them (see templates/_validations.tpl). They are throwaway
// literals used only for rendering: nothing is installed. The key is the one
// docker-compose.yml used to publish, which is as good as any other for a
// render and protects nothing anywhere.
var baseValues = []string{
	"--set", "secrets.masterEncryptionKey=a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=",
	"--set", "secrets.jwtSecret=helm-lint-render-only-not-a-real-secret",
	"--set", "postgresql.auth.password=" + basePostgresPassword,
}

// profile is one configuration the chart must render, plus what the render has
// to look like.
type profile struct {
	// name appears in every finding from this profile.
	name string
	// values are the --set arguments beyond baseValues.
	values []string
	// probeScheme is the scheme the controller's HTTP probes must carry:
	// HTTPS when the controller terminates TLS itself, HTTP when an ingress
	// in front of it did.
	probeScheme string
	// wantDisruptionBudgets is how many PodDisruptionBudgets this profile has
	// to produce.
	//
	// A number rather than a boolean, and asserted rather than counted,
	// because the defect it guards against is an ABSENCE: a budget whose
	// template chose its field by truthiness rendered nothing at all for
	// perfectly ordinary values, and every check that iterates over rendered
	// objects passes when the object is missing.
	wantDisruptionBudgets int
}

// profiles is deliberately more than one. A chart with three TLS modes, two
// backend arrangements and an optional ingress has combinations that only
// render on one path, and a linter that checked the defaults alone would prove
// the defaults alone.
var profiles = []profile{
	{
		// The runner's budget is on by default and the controller's is off,
		// so one is the correct answer here and two would mean the chart
		// started producing a budget over a single replica.
		name:                  "trial install (defaults)",
		values:                nil,
		probeScheme:           "HTTPS",
		wantDisruptionBudgets: 1,
	},
	{
		name: "ingress terminates TLS, controller autoscaled",
		values: []string{
			"--set", "controller.tls.mode=upstream",
			"--set", "controller.replicaCount=3",
			"--set", "controller.autoscaling.enabled=true",
			"--set", "controller.podDisruptionBudget.enabled=true",
			// A scaled-out controller keeps nothing durable of its own: the
			// certificate comes from the ingress and the database holds
			// everything else. The chart refuses the alternative (several
			// replicas sharing one ReadWriteOnce claim), which is asserted
			// as its own refusal below.
			"--set", "controller.persistence.enabled=false",
			"--set", "ingress.enabled=true",
		},
		probeScheme:           "HTTP",
		wantDisruptionBudgets: 2,
	},
	{
		name: "TLS in the pod from a Secret, external database and broker",
		values: []string{
			"--set", "controller.tls.mode=secret",
			"--set", "controller.tls.secretName=pleiades-serving-cert",
			"--set", "postgresql.enabled=false",
			"--set", "nats.enabled=false",
			"--set", "externalDatabase.dsn=postgres://pleiades@db.example.com:5432/pleiades?sslmode=verify-full",
			"--set", "externalNats.url=nats://nats.example.com:4222",
			"--set", "runbooks.configMapName=pleiades-runbooks",
		},
		probeScheme:           "HTTPS",
		wantDisruptionBudgets: 1,
	},
	{
		name: "no persistence anywhere (throwaway demo)",
		values: []string{
			"--set", "controller.persistence.enabled=false",
			"--set", "postgresql.persistence.enabled=false",
			"--set", "nats.persistence.enabled=false",
		},
		probeScheme:           "HTTPS",
		wantDisruptionBudgets: 1,
	},
	{
		// THE ARRANGEMENT THAT USED TO BE REFUSED. Several self-provisioning
		// controllers over one shared directory is now supported, because
		// internal/tlscert's provisioning is lock-free: none of them waits on
		// another and the directory converges on one certificate. The refusal
		// that blocked this cited a read-modify-write race that no longer
		// exists in the product, and it is a positive profile rather than a
		// deleted refusal so that the render is really exercised.
		name: "several self-provisioning controllers sharing one directory",
		values: []string{
			"--set", "controller.replicaCount=3",
			"--set", "controller.persistence.accessMode=ReadWriteMany",
			"--set", "controller.podDisruptionBudget.enabled=true",
		},
		probeScheme:           "HTTPS",
		wantDisruptionBudgets: 2,
	},
	{
		// The arrangement any install that manages real devices needs, and
		// the one nothing rendered until now: the runners get the host keys
		// they verify against. Without this mount every SSH task fails
		// closed, so a chart that could not express it could only produce a
		// fleet whose runbooks all refused (FAILURE_PATTERNS.md #150).
		//
		// It is a profile rather than a unit assertion because the mount and
		// its volume are written in two places twenty lines apart, and the
		// failure mode is a volumeMount naming a volume that no longer
		// exists, which renders fine and is rejected by the API server.
		name: "runners carrying the fleet's host keys",
		values: []string{
			"--set", "runner.knownHosts.configMapName=pleiades-known-hosts",
		},
		probeScheme:           "HTTPS",
		wantDisruptionBudgets: 1,
	},
}

// refusal is a configuration the chart must NOT render, and the words its
// error has to contain.
//
// wantMessage is matched as a substring rather than exactly, so rewording a
// refusal does not fail this tool, but deleting the reason from it does.
type refusal struct {
	name        string
	values      []string
	omitBase    bool
	wantMessage string
}

var refusals = []refusal{
	{
		name:        "a runner with a playbook directory",
		values:      []string{"--set", "runner.playbookDir=/playbooks"},
		wantMessage: "SIBLING CONTAINER",
	},
	{
		name:        "a runner with an Ansible image",
		values:      []string{"--set", "runner.ansibleImage=pleiades/legacy-ansible-runner:dev"},
		wantMessage: "SIBLING CONTAINER",
	},
	{
		// NOT "scaled past one replica", which is now allowed over a shared
		// directory. What is refused is scaling out with NO shared directory:
		// each replica's /data is its own emptyDir, so each mints its own
		// certificate and a new one on every restart, and there is nothing
		// for them to converge on.
		name: "a self-provisioning controller scaled out with no shared directory",
		values: []string{
			"--set", "controller.replicaCount=2",
			"--set", "controller.persistence.enabled=false",
		},
		wantMessage: "nothing for them to converge on",
	},
	{
		name: "a self-provisioning controller autoscaled with no shared directory",
		values: []string{
			"--set", "controller.autoscaling.enabled=true",
			"--set", "controller.persistence.enabled=false",
		},
		wantMessage: "nothing for them to converge on",
	},
	{
		name: "several controller replicas over one ReadWriteOnce volume",
		values: []string{
			"--set", "controller.tls.mode=upstream",
			"--set", "controller.replicaCount=3",
		},
		wantMessage: "ReadWriteOnce volume while more than one controller replica",
	},
	{
		// The access mode that used to slip through. The check named
		// ReadWriteOnce literally, so the STRICTER of the two single-writer
		// modes rendered a release whose extra replicas can never schedule.
		name: "several controller replicas over one ReadWriteOncePod volume",
		values: []string{
			"--set", "controller.tls.mode=upstream",
			"--set", "controller.replicaCount=3",
			"--set", "controller.persistence.accessMode=ReadWriteOncePod",
		},
		wantMessage: "ReadWriteOncePod volume while more than one controller replica",
	},
	{
		name:        "TLS from a Secret with no Secret named",
		values:      []string{"--set", "controller.tls.mode=secret"},
		wantMessage: "controller.tls.secretName is empty",
	},
	{
		name:        "an image tagged latest",
		values:      []string{"--set", "controller.image.tag=latest"},
		wantMessage: "rollback unrepeatable",
	},
	{
		name:        "an image pinned by digest",
		values:      []string{"--set", "runner.image.repository=pleiades/runner@sha256"},
		wantMessage: "pinned by digest",
	},
	{
		name:        "a write-ahead log with nowhere durable to write",
		values:      []string{"--set", "runner.extraEnv[0].name=RUNNER_WAL_DIR", "--set", "runner.extraEnv[0].value=/wal"},
		wantMessage: "durable volume PER REPLICA",
	},
	{
		// The runner touches its heartbeat once per interval, so its age is
		// anywhere from zero to one interval even when everything is
		// perfect. A readiness limit inside that window reports a healthy
		// runner not-ready on the first slow round trip, and every rolling
		// update in the release stalls on runners that are working.
		name:        "a readiness staleness limit inside one heartbeat interval",
		values:      []string{"--set", "runner.heartbeat.readinessStaleAfterSeconds=8"},
		wantMessage: "less than twice runner.heartbeat.intervalSeconds",
	},
	{
		// The same mistake on the probe that RESTARTS the pod, which is the
		// dangerous one: it kills a working runner mid-job on the first slow
		// round trip, then kills its replacement.
		name:        "a liveness staleness limit inside one heartbeat interval",
		values:      []string{"--set", "runner.heartbeat.livenessStaleAfterSeconds=15"},
		wantMessage: "crossing it RESTARTS the pod",
	},
	{
		// Inverted thresholds. A pod restarted before it is ever marked
		// not-ready makes the readiness probe dead weight and turns a broker
		// blip into a fleet-wide restart.
		name: "a liveness limit stricter than the readiness limit",
		values: []string{
			"--set", "runner.heartbeat.readinessStaleAfterSeconds=90",
			"--set", "runner.heartbeat.livenessStaleAfterSeconds=45",
		},
		wantMessage: "before it was ever marked not ready",
	},
	{
		name:        "no encryption key",
		omitBase:    true,
		wantMessage: "secrets.masterEncryptionKey is not set",
	},
	{
		name:     "no JWT secret",
		omitBase: true,
		values: []string{
			"--set", "secrets.masterEncryptionKey=a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=",
			"--set", "postgresql.auth.password=" + basePostgresPassword,
		},
		wantMessage: "secrets.jwtSecret is not set",
	},
	{
		// The budget the setup command raises the liveness window for. By
		// hand, without that, Kubernetes would restart the runner partway
		// through the outage the budget promises to survive.
		name:        "an outage budget longer than the runner's liveness window",
		values:      []string{"--set", "mesh.maxOutageSeconds=7200"},
		wantMessage: "mesh.maxOutageSeconds is 7200",
	},
	{
		// The checksum rolls the pods when an operator's Secret changes. A
		// value that is not one would roll them on every edit or never.
		name: "an existing Secret checksum that is not a checksum",
		values: []string{
			"--set", "secrets.existingSecret=operator-managed-secrets",
			"--set", "secrets.existingSecretChecksum=not-a-checksum",
		},
		wantMessage: "existingSecretChecksum",
	},
	{
		name:        "the in-chart database turned off with nothing to point at",
		values:      []string{"--set", "postgresql.enabled=false"},
		wantMessage: "no external database is configured",
	},
	{
		name:        "the broker turned off with nothing to point at",
		values:      []string{"--set", "nats.enabled=false"},
		wantMessage: "externalNats.url is empty",
	},
	{
		name:        "a mistyped value key",
		values:      []string{"--set", "controller.replicaCounts=3"},
		wantMessage: "schema",
	},
	{
		name:        "a controller on a privileged port",
		values:      []string{"--set", "controller.service.port=443"},
		wantMessage: "schema",
	},
	{
		// networking.k8s.io/v1 requires pathType on every path. The schema
		// requires the key for that reason, so the mistake is caught before a
		// render rather than by an API server rejecting the result.
		name: "an ingress path with no pathType",
		values: []string{
			"--set", "ingress.enabled=true",
			"--set", "ingress.hosts[0].host=pleiades.example.com",
			"--set", "ingress.hosts[0].paths[0].path=/",
		},
		wantMessage: "pathType",
	},
	{
		// The budget that used to vanish. Clearing maxUnavailable without
		// setting minAvailable rendered no PodDisruptionBudget at all, so an
		// operator who believed they had configured disruption protection had
		// none and nothing said so.
		name: "an enabled budget with neither field set",
		values: []string{
			"--set", "runner.podDisruptionBudget.maxUnavailable=null",
		},
		wantMessage: "neither minAvailable nor maxUnavailable is set",
	},
	{
		// The same gap from the other side: an operator who set minAvailable
		// without clearing the default maxUnavailable would produce an object
		// the API server rejects.
		name: "a budget with both fields set",
		values: []string{
			"--set", "controller.podDisruptionBudget.enabled=true",
			"--set", "controller.podDisruptionBudget.minAvailable=2",
		},
		wantMessage: "sets both minAvailable and maxUnavailable",
	},
	{
		// 0 is a real budget (maxUnavailable 0 permits no voluntary eviction),
		// and the old truthiness check read it as "not set", so this pair
		// rendered a budget carrying minAvailable when the operator had asked
		// for the opposite. It is a refusal rather than a profile because both
		// fields end up set: 0 counts.
		name: "a budget with maxUnavailable 0 and minAvailable 1",
		values: []string{
			"--set", "runner.podDisruptionBudget.minAvailable=1",
			"--set", "runner.podDisruptionBudget.maxUnavailable=0",
		},
		wantMessage: "sets both minAvailable and maxUnavailable",
	},
	{
		// Two host key sources mount at the same path, so the chart would
		// have to pick one with nothing but template ordering to justify the
		// choice. What it picked would decide which device keys every SSH
		// task in the fleet trusts, which is too much to decide by accident.
		name: "both a ConfigMap and a Secret of host keys",
		values: []string{
			"--set", "runner.knownHosts.configMapName=pleiades-known-hosts",
			"--set", "runner.knownHosts.secretName=pleiades-host-keys",
		},
		wantMessage: "both name a host key source",
	},
}
