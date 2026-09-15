// Package schema's Project: a source control repository this platform
// clones, and the playbooks inside it a Template can then run.
//
// It is the missing source of automation. Until it existed a Template
// could only point at something compiled into the binary, so everything
// runnable had to be written by somebody with commit access to this
// module.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Project holds the schema definition for the Project entity: a source
// control repository this platform clones, and the playbooks inside it that
// a Template can then run.
//
// It is the missing source of automation. Until now a Template could only
// point at something already compiled into the binary through
// launch.Catalog, which means every runnable thing had to be written by
// somebody with commit access to this module. A Project is how work that
// lives in somebody else's repository becomes runnable here, and it is the
// same layer AWX puts between an Organization and a Job Template.
//
// # Why the clone is recorded rather than rediscovered
//
// local_path and revision are stored rather than derived from the working
// tree on disk. A controller is not the only thing that can touch that
// directory, the directory can be lost entirely (a replaced pod, an evicted
// volume), and a Template that names a playbook needs a stable answer to
// "which commit was that" even when the checkout is gone. Storing the
// answer makes a missing tree a detectable state rather than an empty
// listing that looks like a repository with no playbooks in it.
//
// # Credentials
//
// A private repository needs one, and it is an ordinary Credential of the
// scm kind rather than a pair of fields here. That is deliberate: a
// username and a token stored on this row would be a second secret store
// with its own encryption, rotation and redaction story, sitting beside the
// one that already has all three.
type Project struct {
	ent.Schema
}

// Mixin of the Project.
func (Project) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Project.
func (Project) Fields() []ent.Field {
	return []ent.Field{
		// Unique per organization rather than globally, for the reason
		// Inventory's own name field gives: two tenants both having a
		// "playbooks" project is the ordinary case.
		field.String("name").
			NotEmpty().
			MaxLen(253),

		field.String("description").
			Default("").
			MaxLen(1024),

		// scm_type is how the source is reached. Only "git" is implemented;
		// the column exists because AWX's own project has archive, svn and
		// manual alongside it, and a deployment importing one needs
		// somewhere to record what it was rather than having the import
		// silently call it git.
		field.Enum("scm_type").
			Values("git", "archive", "manual").
			Default("git"),

		field.String("scm_url").
			Default("").
			MaxLen(2048),

		// The branch, tag or commit to check out. Empty means the remote's
		// own default branch, which is not assumed to be "main": a
		// repository whose default is "master" or "trunk" is not this
		// platform's business to rename.
		field.String("scm_branch").
			Default("").
			MaxLen(255),

		// Where the working tree lives, keyed by id rather than by name so
		// a rename does not orphan a checkout.
		field.String("local_path").
			Default("").
			MaxLen(4096),

		// The commit the working tree is at, as a full hash. Empty until a
		// sync has succeeded at least once.
		field.String("revision").
			Default("").
			MaxLen(64),

		field.Enum("sync_status").
			Values("never", "pending", "running", "succeeded", "failed").
			Default("never"),

		// Why the last sync failed. It is shown to an operator, so it must
		// never carry credential material: internal/project is responsible
		// for stripping a URL's userinfo before an error reaches here.
		field.String("sync_error").
			Default("").
			MaxLen(2048),

		field.Time("last_synced_at").
			Optional().
			Nillable(),
	}
}

// Edges of the Project.
func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		// A Project MUST belong to exactly one Organization, for the reason
		// Inventory's own organization edge gives: it is the tenancy
		// boundary, and a project belonging to none could be resolved
		// against no organization scope.
		edge.From("organization", Organization.Type).
			Ref("projects").
			Unique().
			Required(),

		// The credential that authenticates the clone, when the repository
		// is private. Optional, because a public repository needs none, and
		// Unique because a clone authenticates as one identity.
		edge.From("credential", Credential.Type).
			Ref("projects").
			Unique(),

		// The templates that run playbooks out of this project.
		edge.To("templates", Template.Type),

		// Every completed attempt to fetch this project's source. The latest
		// outcome stays on this row; these are the history behind it.
		edge.To("sync_runs", SyncRun.Type),
	}
}

// Indexes of the Project.
func (Project) Indexes() []ent.Index {
	return []ent.Index{
		// Name is unique within an organization, not globally. This is the
		// constraint, not merely a lookup index.
		index.Fields("name").
			Edges("organization").
			Unique(),
	}
}
