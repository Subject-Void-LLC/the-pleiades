// Package view's settings area: the deployment's own configuration.
//
// Entirely declared. This platform has no settings store, so every tile
// describes something currently fixed by the composition root or the
// environment, and says which.
package view

import (
	"net/url"
	"path"
	"strings"
)

// System settings: the deployment's own configuration, as against a caller's
// own preferences next door in account.go.
//
// Every tile below is declared and none is implemented, and that is the honest
// state rather than a staging post. This platform has no settings store at
// all: no entity, no endpoint in internal/apispec, no scope until this file
// added one. Everything a tile describes is currently a composition-root or
// environment decision, fixed when cmd/controller starts and unchangeable
// without restarting it. The environment banner, the session ceiling, the page
// size and the log destination are all real and all decided there.
//
// Declaring the whole surface anyway is the same argument LESSONS_LEARNED #175
// makes for the declared views: the shape is going to be decided by whoever
// writes the store, and it is far cheaper to disagree with it here. It also
// makes a specific and useful claim to anyone evaluating this against AWX --
// this is the list of settings that exist there and do not exist here, with
// the reason for each.
//
// The fields are AWX's own, named as AWX names them, because an operator
// arriving from AWX is the audience and a renamed field is one they have to
// translate. Where this platform has a different word for the same thing, the
// help text says so rather than the label.

// SettingsGroup is one block of related settings inside a tile: LDAP inside
// Authentication, the log destination inside Logging.
//
// AWX calls these sub-tiles and reaches them by drilling into a tile; they are
// fieldsets on the tile's own page here, because none of them is large enough
// on its own to be worth a second navigation and a reader comparing LDAP with
// SAML should not have to hold one in their head to look at the other.
type SettingsGroup struct {
	Title   string
	Summary string

	// Status is this group's own, not its tile's. A tile may be partly real:
	// nothing is today, and the type does not assume that stays true.
	Status Status

	// Reason says what specifically is missing, in the same voice a declared
	// section's Empty uses. Never "not implemented": that is the one thing
	// the reader can already see.
	Reason string

	Fields []Field
}

// Implemented reports whether this group is backed by anything.
func (g SettingsGroup) Implemented() bool { return g.Status == StatusImplemented }

// ID is the group's DOM identifier, for the heading its fieldset is labelled
// by. Derived from the title through the same slug rule tabs use, so a group
// cannot disagree with itself about its own name.
func (g SettingsGroup) ID() string { return "settings-" + TabSlug(g.Title) }

// SettingsTile is one page of the settings area, and one tab.
type SettingsTile struct {
	Title   string
	Summary string
	Status  Status
	Groups  []SettingsGroup
}

// Slug is this tile's address in the tab query parameter.
func (t SettingsTile) Slug() string { return TabSlug(t.Title) }

// Implemented reports whether anything on this tile is backed.
func (t SettingsTile) Implemented() bool { return t.Status == StatusImplemented }

// SystemSettingsModel is the settings area.
//
// A fixed route with no record id, like the account page, because settings are
// a singleton: there is one deployment and it has one configuration. The
// authorization is a scope rather than the structural "the session is the
// subject" that protects the account page, because this surface is about
// everyone.
type SystemSettingsModel struct {
	Page PageModel

	// Tab is the requested tile, unvalidated. CurrentTab resolves it.
	Tab string
}

// Tiles is the settings area, in the order AWX lists them.
//
// A method rather than a package-level variable so the reasons can name this
// deployment's own state, and so nothing can mutate the list at runtime.
func (m SystemSettingsModel) Tiles() []SettingsTile {
	return []SettingsTile{
		authenticationTile(),
		jobsTile(),
		systemTile(),
		userInterfaceTile(),
		loggingTile(),
	}
}

// CurrentTab is the selected tile's slug, defaulting to the first.
//
// An unrecognised value falls back rather than rendering an empty page: the
// value arrives in a query parameter, so it arrives from whatever somebody
// pasted into an address bar.
func (m SystemSettingsModel) CurrentTab() string {
	want := strings.TrimSpace(m.Tab)
	tiles := m.Tiles()
	for _, t := range tiles {
		if t.Slug() == want {
			return want
		}
	}
	return tiles[0].Slug()
}

// CurrentTile is the tile being rendered.
func (m SystemSettingsModel) CurrentTile() SettingsTile {
	current := m.CurrentTab()
	for _, t := range m.Tiles() {
		if t.Slug() == current {
			return t
		}
	}
	return m.Tiles()[0]
}

// SettingsHref is where the navigation's Settings entry points.
func (p PageModel) SettingsHref() string { return path.Join(p.Prefix, "settings") }

// Chrome is the settings area's header, built the same way every other page's
// is so this surface cannot drift from the rest of the application.
func (m SystemSettingsModel) Chrome() Chrome {
	base := m.Page.SettingsHref()
	current := m.CurrentTab()

	tiles := m.Tiles()
	tabs := make([]Tab, 0, len(tiles))
	for i, t := range tiles {
		href := base
		if i > 0 {
			href = base + "?tab=" + url.QueryEscape(t.Slug())
		}
		tabs = append(tabs, Tab{
			Label:   t.Title,
			Slug:    t.Slug(),
			Href:    href,
			Current: t.Slug() == current,
		})
	}

	return Chrome{
		Crumbs: []Crumb{
			{Label: "Administration"},
			{Label: "Settings", Current: true},
		},
		Title: "Settings",
		Summary: "How this deployment behaves, for everyone who uses it. " +
			"Your own appearance and password are on the Preferences page instead.",
		Badges: []Badge{{Label: "Declared", Class: "badge-skipped"}},
		Tabs:   tabs,
	}
}

// Declared is the panel shown above every tile.
//
// One statement for the whole area rather than one per group, because the
// reason is the same everywhere and repeating it six times would train a
// reader to stop reading it.
func (m SystemSettingsModel) Declared() ZeroState {
	return ZeroState{
		Heading: "Declared, not implemented",
		Body: "Nothing on this page is editable. This deployment has no settings store: " +
			"every value below is fixed by the composition root or the environment when " +
			"cmd/controller starts, and changing one means restarting it. The shape is " +
			"declared here so it can be argued with before it is built.",
		Tone: ZoneDeclared,
	}
}

// authenticationTile is how somebody proves who they are.
//
// What this deployment actually has: a local password store (Argon2id, through
// internal/localauth) and a bearer token from an external issuer, exchanged for
// a session cookie at sign-in. Neither is configurable from anywhere but the
// composition root, and none of the enterprise directory integrations below
// exists in any form.
func authenticationTile() SettingsTile {
	return SettingsTile{
		Title:  "Authentication",
		Status: StatusDeclared,
		Summary: "How users prove who they are, and how a directory's groups become roles here. " +
			"This deployment authenticates a local password or a token from an external issuer, and nothing else.",
		Groups: []SettingsGroup{
			{
				Title:   "LDAP",
				Summary: "The most common enterprise integration: bind a service account, search the directory, map the groups it finds onto organizations and teams.",
				Status:  StatusDeclared,
				Reason: "There is no directory client in this build. The mapping fields matter more than the connection ones: " +
					"this platform grants roles to teams and never to a person, so a group mapping is the whole provisioning story.",
				Fields: []Field{
					{Name: "ldap_server_uri", Label: "LDAP SERVER URI", Kind: KindText, Help: "The address of the Active Directory or LDAP server, including scheme and port."},
					{Name: "ldap_bind_dn", Label: "BIND DN", Kind: KindText, Help: "The service account used to search the directory."},
					{Name: "ldap_bind_password", Label: "BIND PASSWORD", Kind: KindPassword, Help: "Write-only. Stored encrypted and never rendered back, as every credential in this platform already is."},
					{Name: "ldap_user_search", Label: "USER SEARCH", Kind: KindLongText, Help: "The query structure that finds a user, as a base DN, a scope and a filter."},
					{Name: "ldap_group_search", Label: "GROUP SEARCH", Kind: KindLongText, Help: "The query structure that finds the groups a user belongs to."},
					{Name: "ldap_organization_map", Label: "ORGANIZATION MAP", Kind: KindLongText, Help: "JSON mapping directory groups onto organizations. Would provision membership on sign-in rather than by hand."},
					{Name: "ldap_team_map", Label: "TEAM MAP", Kind: KindLongText, Help: "JSON mapping directory groups onto teams. A team is what holds a role binding here, so this is where RBAC provisioning would happen."},
				},
			},
			{
				Title:   "SAML",
				Summary: "Enterprise single sign-on through an identity provider such as Okta, PingIdentity or ADFS.",
				Status:  StatusDeclared,
				Reason: "No SAML implementation exists. The sign-in page's token field is the nearest thing: " +
					"it accepts a bearer token an external issuer already minted, which covers the same ground for an issuer that can mint one.",
				Fields: []Field{
					{Name: "saml_idp_entity_id", Label: "IDP ENTITY ID", Kind: KindText, Help: "The identity provider's unique identifier."},
					{Name: "saml_sso_url", Label: "SSO URL", Kind: KindText, Help: "Where a sign-in is redirected to."},
					{Name: "saml_x509_cert", Label: "IDP X.509 CERTIFICATE", Kind: KindLongText, Help: "The provider's public certificate, used to verify assertion signatures."},
					{Name: "saml_sp_entity_id", Label: "SERVICE PROVIDER ENTITY ID", Kind: KindText, Help: "This deployment's own identifier, which the provider is configured with."},
					{Name: "saml_org_map", Label: "ORGANIZATION MAP", Kind: KindLongText, Help: "JSON mapping assertion attributes onto organizations."},
					{Name: "saml_team_map", Label: "TEAM MAP", Kind: KindLongText, Help: "JSON mapping assertion attributes onto teams."},
				},
			},
			{
				Title:   "OAuth providers",
				Summary: "Azure AD, GitHub and Google OAuth2. One block of keys each, plus the mapping from the provider's own groups onto organizations here.",
				Status:  StatusDeclared,
				Reason:  "No OAuth client exists in this build. Each provider needs its own callback URL, which is why the base URL under System has to be right before any of these can work at all.",
				Fields: []Field{
					{Name: "oauth_provider", Label: "PROVIDER", Kind: KindSelect, Help: "Azure AD, GitHub or Google. Each carries the same four fields below."},
					{Name: "oauth_client_id", Label: "CLIENT ID", Kind: KindText, Help: "The application identifier issued by the provider."},
					{Name: "oauth_client_secret", Label: "CLIENT SECRET", Kind: KindPassword, Help: "Write-only, stored encrypted."},
					{Name: "oauth_callback_url", Label: "CALLBACK URL", Kind: KindReadOnly, Help: "Derived from the base URL under System and shown here to be copied into the provider's own configuration. Read-only by construction: a callback this deployment cannot serve is worse than none."},
					{Name: "oauth_organization_map", Label: "ORGANIZATION MAP", Kind: KindLongText, Help: "JSON mapping the provider's organizations or groups onto organizations here."},
				},
			},
			{
				Title:   "RADIUS and TACACS+",
				Summary: "Legacy authentication endpoints, still the normal answer for a network engineering team.",
				Status:  StatusDeclared,
				Reason: "Neither protocol is implemented. Worth more here than the AWX equivalent is worth there, " +
					"because this platform's network device support is a first-class path rather than an afterthought, and the teams who run those devices are the ones who authenticate this way.",
				Fields: []Field{
					{Name: "radius_server", Label: "SERVER", Kind: KindText, Help: "The address of the RADIUS or TACACS+ server."},
					{Name: "radius_port", Label: "PORT", Kind: KindNumber, Help: "1812 for RADIUS authentication, 49 for TACACS+, by convention."},
					{Name: "radius_secret", Label: "SHARED SECRET", Kind: KindPassword, Help: "Write-only, stored encrypted."},
				},
			},
			{
				Title:   "Sessions and API access",
				Summary: "What a session is worth and how long it lasts. The one group here with real behaviour behind it, fixed at startup rather than set here.",
				Status:  StatusDeclared,
				Reason: "These are real today and are decided by the composition root. A session is a bearer credential with a ceiling, " +
					"sign-in is rate limited, and the API accepts a bearer token; none of it is editable at runtime, which is what this group would change.",
				Fields: []Field{
					{Name: "session_timeout", Label: "SESSION TIMEOUT", Kind: KindNumber, Help: "Seconds a session survives. Real and enforced today; the ceiling is set when the controller starts."},
					{Name: "session_auth", Label: "ALLOW BROWSER SESSIONS", Kind: KindBool, Help: "Whether a cookie session may call the API at all. On today, and the reason the UI works."},
					{Name: "basic_auth", Label: "ALLOW BASIC AUTH", Kind: KindBool, Help: "Whether a username and password may be presented directly to an API request rather than exchanged for a session first."},
					{Name: "login_rate_limit", Label: "SIGN-IN RATE LIMIT", Kind: KindNumber, Help: "Attempts per window before sign-in is shed. Real today, and it runs before the CSRF check so a flood is dropped before it can cost a password derivation."},
				},
			},
		},
	}
}

// jobsTile is the global execution behaviour every run inherits.
//
// The closest tile to something this platform could implement soon: launch
// kinds already declare per-run fields for several of these, so the missing
// piece is a deployment-wide default rather than the concept.
func jobsTile() SettingsTile {
	return SettingsTile{
		Title:  "Jobs",
		Status: StatusDeclared,
		Summary: "The guardrails every run inherits. Several of these exist per launch already, declared by the launch kind; " +
			"what is missing is a deployment-wide default a template can narrow rather than a value chosen afresh each time.",
		Groups: []SettingsGroup{
			{
				Title:   "Limits and timeouts",
				Summary: "How long the platform waits, and how much it runs at once.",
				Status:  StatusDeclared,
				Reason:  "The runbook and playbook launch kinds already declare TIMEOUT and FORKS as per-launch fields, so a launching operator sets them every time and nothing bounds what they may set.",
				Fields: []Field{
					{Name: "default_job_timeout", Label: "DEFAULT JOB TIMEOUT", Kind: KindNumber, Help: "Seconds before a run is abandoned. Per launch today, with no deployment-wide ceiling."},
					{Name: "default_forks", Label: "DEFAULT FORKS", Kind: KindNumber, Help: "Devices in flight at once. Per launch today."},
					{Name: "project_update_timeout", Label: "PROJECT UPDATE TIMEOUT", Kind: KindNumber, Help: "Seconds to wait for a repository to sync. Nothing yet: projects are a declared view with no port."},
					{Name: "fact_cache_timeout", Label: "FACT CACHE TIMEOUT", Kind: KindNumber, Help: "How long gathered facts stay usable. Nothing yet: facts are returned to the job that asked and are never stored."},
				},
			},
			{
				Title:   "Ad hoc commands",
				Summary: "Which methods a caller may run directly against a device without a saved definition.",
				Status:  StatusDeclared,
				Reason: "This platform has no ad hoc path at all, which is a stronger guarantee than the allowlist it would replace: " +
					"every dispatch goes through a template, so what may run is already bounded by what somebody saved and who may edit it.",
				Fields: []Field{
					{Name: "allowed_adhoc", Label: "ALLOWED METHODS", Kind: KindTags, Help: "The fully-qualified collection names a caller could run directly, if an ad hoc path existed. exec.command and net.cli.command are the ones that would need arguing about."},
				},
			},
			{
				Title:   "Isolation",
				Summary: "What a running task can reach on the machine running it.",
				Status:  StatusDeclared,
				Reason: "Partly real and not a toggle. Every Collection method already runs in a per-task subprocess so a secret crosses a real process boundary on stdin rather than through argv or the environment, " +
					"and an Ansible playbook runs inside a container. Neither is optional, which is why there is nothing here to switch off.",
				Fields: []Field{
					{Name: "job_isolation", Label: "ISOLATE EXECUTION", Kind: KindBool, Help: "Whether a task may reach the controller's own filesystem. Always on here, and not currently expressible as off."},
					{Name: "isolation_show_paths", Label: "EXPOSED PATHS", Kind: KindTags, Help: "Paths deliberately made visible inside an isolated run."},
				},
			},
			{
				Title:   "Output and variables",
				Summary: "What a run is given, and how its output reads.",
				Status:  StatusDeclared,
				Reason:  "Extra variables are per launch and per survey today. The colour setting has a real consumer waiting: the job record's Output tab renders a live stream that currently carries no ANSI handling.",
				Fields: []Field{
					{Name: "inject_job_vars", Label: "INJECT PLATFORM VARIABLES", Kind: KindBool, Help: "Whether the job id, the launching actor and the template name are merged into every run as extra variables."},
					{Name: "ansi_color", Label: "RENDER ANSI COLOUR", Kind: KindBool, Help: "Whether the Output tab interprets the red, green and yellow a CLI emits, rather than showing the escape codes."},
					{Name: "extra_vars", Label: "GLOBAL EXTRA VARIABLES", Kind: KindLongText, Help: "Variables merged into every run in the deployment, under a template's own and under a survey's answers."},
				},
			},
		},
	}
}

// systemTile is the appliance itself.
func systemTile() SettingsTile {
	return SettingsTile{
		Title:   "System",
		Status:  StatusDeclared,
		Summary: "The deployment's own identity, how it reaches the outside world, and how long it keeps what it records.",
		Groups: []SettingsGroup{
			{
				Title:   "Base URL",
				Summary: "The fully qualified address of this deployment.",
				Status:  StatusDeclared,
				Reason: "Nothing stores it. It is load-bearing rather than cosmetic: every callback URL handed to an external identity provider is built from it, " +
					"so a wrong value here is an authentication integration that fails in a way nobody can debug from the provider's side.",
				Fields: []Field{
					{Name: "base_url", Label: "BASE URL", Kind: KindText, Help: "For example https://pleiades.example.com. Dictates the callback URLs sent to SAML and OAuth providers."},
				},
			},
			{
				Title:   "Outbound proxy",
				Summary: "How this deployment reaches the internet when it sits behind a corporate firewall.",
				Status:  StatusDeclared,
				Reason:  "No proxy configuration is read anywhere. Go's own HTTP client honours HTTP_PROXY and HTTPS_PROXY from the environment, so a deployment behind a firewall is configured by its process environment rather than here.",
				Fields: []Field{
					{Name: "http_proxy", Label: "HTTP PROXY", Kind: KindText, Help: "Address and port."},
					{Name: "https_proxy", Label: "HTTPS PROXY", Kind: KindText, Help: "Address and port."},
					{Name: "no_proxy", Label: "BYPASS", Kind: KindTags, Help: "Hosts reached directly. Device addresses belong here: a proxy has no business between this platform and the fleet it manages."},
				},
			},
			{
				Title:   "Retention",
				Summary: "How long the database keeps what it records before it is purged.",
				Status:  StatusDeclared,
				Reason: "Nothing purges anything. The activity stream is append-only and grows without bound, job history is kept indefinitely, " +
					"and the only sweeper in the build is the fan-out reaper, which reclaims stale leases rather than reclaiming disk.",
				Fields: []Field{
					{Name: "retain_jobs_days", Label: "KEEP JOB HISTORY", Kind: KindNumber, Help: "Days. A job is the audit record of a change to the fleet, so this is a compliance decision before it is a disk one."},
					{Name: "retain_activity_days", Label: "KEEP ACTIVITY STREAM", Kind: KindNumber, Help: "Days. The append-only record of who changed what."},
					{Name: "retain_facts_days", Label: "KEEP FACT HISTORY", Kind: KindNumber, Help: "Days. Nothing stores facts yet, so this would arrive with a fact cache rather than before one."},
				},
			},
			{
				Title:   "Telemetry",
				Summary: "What, if anything, this deployment reports about itself to anyone else.",
				Status:  StatusDeclared,
				Reason:  "Nothing is sent anywhere and there is no upstream to send it to. The setting is declared so the answer is visible rather than assumed, which matters most to the deployments least able to allow it.",
				Fields: []Field{
					{Name: "analytics_enabled", Label: "SEND USAGE ANALYTICS", Kind: KindBool, Help: "Off, and there is no code path that would turn it on."},
				},
			},
		},
	}
}

// userInterfaceTile is the front end, and the tile with the most real
// behaviour already behind it.
func userInterfaceTile() SettingsTile {
	return SettingsTile{
		Title:  "User interface",
		Status: StatusDeclared,
		Summary: "What this deployment looks like to everyone, as against the appearance each person chooses for themselves. " +
			"The banner and the page size are real and enforced; what is missing is a way to change them without a restart.",
		Groups: []SettingsGroup{
			{
				Title:   "Login banner",
				Summary: "The notice shown before anybody authenticates.",
				Status:  StatusDeclared,
				Reason: "Real and enforced, and set at the composition root. The environment and classification banner already renders at the top AND bottom of every page including the sign-in page, " +
					"from a closed vocabulary of markings with their published colour pairs, because a marking that scrolls out of view is not on the part of the screen being read. What is missing is editing it here.",
				Fields: []Field{
					{Name: "banner_level", Label: "MARKING", Kind: KindSelect, Help: "One of the declared environment or classification markings. A closed vocabulary rather than free text, deliberately: a marking is a safety statement and an unrecognised one must fail rather than render."},
					{Name: "login_notice", Label: "CUSTOM LOGIN NOTICE", Kind: KindLongText, Help: "Free text shown on the sign-in page, for a legal warning or a maintenance notice. Nothing renders this today."},
				},
			},
			{
				Title:   "Branding",
				Summary: "Replacing this platform's own marks with an organization's.",
				Status:  StatusDeclared,
				Reason:  "No upload path and nowhere to put a file: the brand is text in the sidebar and the favicon is a content-hashed static asset compiled into the binary.",
				Fields: []Field{
					{Name: "custom_logo", Label: "LOGO", Kind: KindText, Help: "An image shown in place of the wordmark in the sidebar."},
					{Name: "custom_name", Label: "DEPLOYMENT NAME", Kind: KindText, Help: "Shown beside or instead of the wordmark. Useful well before a logo is: it is how somebody with staging and production open at once tells the two apart."},
				},
			},
			{
				Title:   "Density and paging",
				Summary: "How much is on a page.",
				Status:  StatusDeclared,
				Reason:  "The default page size is a constant in the UI handler, and a reader may already override it per request with a limit parameter between 1 and 200. What is missing is a deployment default other than the compiled-in one.",
				Fields: []Field{
					{Name: "page_size", Label: "ITEMS PER PAGE", Kind: KindNumber, Help: "The default number of rows a collection shows. Bounded at 200 per request today, whatever is asked for."},
				},
			},
		},
	}
}

// loggingTile is where this deployment's own logs go.
func loggingTile() SettingsTile {
	return SettingsTile{
		Title:  "Logging",
		Status: StatusDeclared,
		Summary: "Forwarding this deployment's logs to a central platform. Everything is already structured and already carries the trace context; " +
			"what is missing is a destination other than the process's own output.",
		Groups: []SettingsGroup{
			{
				Title:   "Destination",
				Summary: "Where logs are sent.",
				Status:  StatusDeclared,
				Reason: "There is no forwarder. Logging is structured through log/slog and written to the process's own output, which a container runtime collects; " +
					"a deployment that wants Splunk or Elastic collects it there rather than here.",
				Fields: []Field{
					{Name: "logging_enabled", Label: "ENABLE EXTERNAL LOGGING", Kind: KindBool, Help: "The master switch. Off, with no code path behind it."},
					{Name: "logging_type", Label: "AGGREGATOR", Kind: KindSelect, Help: "Splunk, Logstash, Datadog or a generic HTTP endpoint."},
					{Name: "logging_url", Label: "AGGREGATOR URL", Kind: KindText, Help: "The endpoint logs are posted to."},
					{Name: "logging_token", Label: "AGGREGATOR TOKEN", Kind: KindPassword, Help: "Write-only, stored encrypted."},
					{Name: "logging_verify_ssl", Label: "VERIFY CERTIFICATE", Kind: KindBool, Help: "Whether the aggregator's certificate is checked. Defaulting this to off is how a log pipeline quietly becomes a plaintext one."},
				},
			},
			{
				Title:   "What to forward",
				Summary: "Which datasets leave this deployment, chosen individually because they differ enormously in volume and in sensitivity.",
				Status:  StatusDeclared,
				Reason: "Two of these exist as real, separable datasets today: the activity stream is a queryable append-only store, and per-device task outcomes are recorded per job. " +
					"Neither has a forwarder. Job events are the one to think about before enabling: one row per task per device is the highest-volume thing this platform produces.",
				Fields: []Field{
					{Name: "log_activity_stream", Label: "ACTIVITY STREAM", Kind: KindBool, Help: "Who changed which managed object. Low volume, high value, and the dataset an audit actually asks for."},
					{Name: "log_job_events", Label: "JOB EVENTS", Kind: KindBool, Help: "Every task result on every device. Extremely high volume: a single fleet-wide sweep across 1,200 devices produces 1,200 rows per task."},
					{Name: "log_system_tracking", Label: "SYSTEM TRACKING", Kind: KindBool, Help: "How gathered facts change over time. Nothing stores facts yet, so this would arrive with a fact cache."},
					{Name: "log_platform", Label: "PLATFORM LOGS", Kind: KindBool, Help: "This deployment's own errors and request logs."},
				},
			},
			{
				Title:   "Format",
				Summary: "The shape of an exported record.",
				Status:  StatusDeclared,
				Reason:  "Records are structured slog attributes today and their shape is whatever each call site passes. A stated schema is worth having before a forwarder, not after: a consumer that has started parsing a shape is a consumer the shape can no longer change for.",
				Fields: []Field{
					{Name: "log_schema", Label: "RECORD SCHEMA", Kind: KindLongText, Help: "JSON describing the fields an exported record carries."},
				},
			},
		},
	}
}

// SettingsCurrent reports whether the navigation's Settings entry is the page
// being rendered, which is what carries aria-current.
//
// It reads Title rather than a name field because the settings area is not a
// registered view and therefore has no descriptor name for BuildNavSections to
// have marked current. Comparing the rendered title is the same thing that
// function does with a descriptor's, one layer out.
func (p PageModel) SettingsCurrent() bool { return p.Title == "Settings" }
