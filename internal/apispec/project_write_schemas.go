// This file holds the bodies the two project writes accept: POST /projects
// and PATCH /projects/{id}.
//
// Each length bound is read from internal/project, whose constants a test
// holds equal to the database's own validators, so the document cannot state
// a bound the server does not enforce.
package apispec

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// projectBound describes a text field's limit in bytes, after trimming.
func projectBound(limit int) string {
	return fmt.Sprintf(" At most %d bytes once trimmed; a longer value is refused with 400.", limit)
}

// projectSourceRules is what scm_url says on both writes.
const projectSourceRules = "The repository to clone. Fetched over https or ssh; plain http, the git daemon " +
	"protocol and paths on the server's own disk are refused unless the deployment has opted into them. A " +
	"URL that may carry a credential is refused for a project of every type, manual and archive included: " +
	"any \"@\" outside one ssh login (ssh://user@host/... or user@host:path, with no password), " +
	"written, percent-encoded or a Unicode lookalike, and any query or fragment, since a repository source " +
	"needs neither and a presigned URL is itself a credential. Give the project a credential instead."

// projectWriteSchema is the body POST /projects accepts.
var projectWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "organization"},
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization." + projectBound(project.MaxNameBytes)),
		"description":  stringSchema("What is in this repository." + projectBound(project.MaxDescriptionBytes)),
		"organization": map[string]any{"type": "integer", "description": "The tenancy boundary this project belongs to. Not updatable."},
		"scm_type":     stringSchema("git, archive or manual. Empty or absent means git."),
		"scm_url":      stringSchema(projectSourceRules + " Required for git." + projectBound(project.MaxSCMURLBytes)),
		"scm_branch":   stringSchema("The branch, tag or commit to check out. Empty means the remote's own default." + projectBound(project.MaxSCMBranchBytes)),
		"credential":   map[string]any{"type": "integer", "description": "A source control credential carrying a password, token or SSH key, owned by the project's own organization; another organization's is refused as if it did not exist. 0 or absent for a public repository."},
	},
}

// projectPatchSchema is the body PATCH /projects/{id} accepts.
var projectPatchSchema = map[string]any{
	"type": "object",
	"description": "A partial update. A field left out keeps its stored value and a field present is applied; an " +
		"empty value clears description, scm_url and scm_branch, and \"credential\": 0 unbinds the credential. No " +
		"field may be null: leave it out to keep it. The project as it would be saved is checked whole, so " +
		"setting scm_type to git on a project with no scm_url is refused, and a kept credential is checked again. " +
		"Unknown fields, the read-only sync fields among them, are refused, and so is a field named twice in any " +
		"mix of upper and lower case.",
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization. May not be blank." + projectBound(project.MaxNameBytes)),
		"description":  stringSchema("What is in this repository. \"\" clears it." + projectBound(project.MaxDescriptionBytes)),
		"organization": map[string]any{"type": "integer", "description": "Not updatable: accepted only as the stored value, and any other is refused with 400."},
		"scm_type":     stringSchema("git, archive or manual. Empty means git, as on create."),
		"scm_url": stringSchema(projectSourceRules + " A git project may not clear it. GET shows a URL that carries a " +
			"credential redacted or withheld whole, and sending back exactly what GET showed keeps the stored URL." +
			projectBound(project.MaxSCMURLBytes)),
		"scm_branch": stringSchema("The branch, tag or commit to check out. \"\" means the remote's own default." + projectBound(project.MaxSCMBranchBytes)),
		"credential": map[string]any{"type": "integer", "description": "Rebinds the project to this source control credential, which the project's own organization must own. 0 unbinds it; leaving the key out keeps it, and a kept credential is checked again."},
	},
}
