// This file holds the protocol-neutral YANG methods' catalog entries:
// net.yang.schema, word for word its registered documentation
// (internal/catalog/net/yang's Doc; internal/archtest's
// TestCatalogDataDocsMatchTheRegistry) and its alternatives as it registers
// them (TestCatalogDataAlternativesMatchTheRegistry).
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// yangCollections are the protocol-neutral YANG methods.
var yangCollections = []collectionscaffold.Config{
	// net.yang.schema saves the YANG source of a device's modules into the
	// project, fetched over NETCONF or RESTCONF, whichever the device serves
	// (NETCONF first) or the task names with via.
	{
		Name:         "net.yang.schema",
		Capabilities: []capability.Name{},
		Transports:   []string{"netconf", "restconf"},
		Alternatives: []collection.Alternative{
			{Name: "netconf", SupportedTransports: []string{"netconf"}, RequiredCapabilities: []capability.Name{capability.NameNetconf, capability.NameSSHTransport}},
			{Name: "restconf", SupportedTransports: []string{"restconf"}, RequiredCapabilities: []capability.Name{capability.NameRestconf}},
		},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Fetches the YANG source of a device's modules from the device and saves each into the project.",
			Description: "Reads the device's YANG library and fetches the source of each module named in modules (or of every module the library lists, implemented and import-only, up to max_modules), with each module's submodules, from the device itself, and writes each into the project under dest as name@revision.yang, so an author or an agent can read the schema the device really serves. " +
				"Over NETCONF it asks the device with RFC 6022's get-schema, which a device offers when its hello or YANG library lists ietf-netconf-monitoring. Over RESTCONF it fetches the URL the device's YANG library names for each source (RFC 8040 section 3.7: RFC 7895's schema leaf, or RFC 8525's location), reading modules-state for those URLs when the RFC 8525 library names none, as IOS XE 17.15's does; " +
				"a URL at another scheme, host or port than the device's own base URL is refused, since fetching it would send the device's credential elsewhere, and a device whose library names no URL (rousette names none) is refused naming that. " +
				"A device serving both protocols is read over NETCONF unless via says otherwise, chosen at plan time: a task naming a protocol the device does not serve is refused by validate, and a fetch that fails over one protocol is the task's failure, never a try over the other. gNMI defines no way to fetch a module's source, so a device served only by gNMI is refused. " +
				"Every answer is held to being the source asked for (its module or submodule statement names the module, and its revision statements include the revision asked for) before anything is written, and every source is fetched before any file is written, so a task fails whole rather than halfway. " +
				"A source is schema, not configuration, so it is never masked by shape: a leaf named password, a list's key statement and the word secret are kept as the device sent them. It is masked by value only, each of the run's known secret values (the device's credential and the project's named secrets) of at least 8 bytes replaced wherever it stands by a pleiades-masked- placeholder; a shorter value cannot be told from a word of the schema and is not masked, which the task warns. " +
				"The run's report names modules, files, sizes and SHA-256 digests, never a source's text. File names are built from the library's YANG identifier and revision date alone, never from other device text, and two names one case-insensitive file system would take for one file are refused. It runs on the command line only (pleiades run and adhoc), which holds the project; a Walk-tier Runner refuses it at validation. A check reads the library and fetches every source, both reads, and reports which files a run would write, writing none. It changes nothing on the device and cannot be undone by a rollback: the files' earlier content is never journaled, since the project is the author's working copy, kept under version control.",
			Params: []collection.Param{
				{Name: "modules", Type: "list of string", Description: "The modules to fetch, each a name or name@revision as the device's YANG library lists it, such as ietf-interfaces or Cisco-IOS-XE-native@2024-07-01; a submodule may be named too. A name the library lists at two revisions is refused until one is named. Leave it out to fetch every module the library lists."},
				{Name: "submodules", Type: "bool", Default: "true", Description: "Fetch each module's submodules beside it, as the library lists them; a module that includes submodules cannot be read whole without them."},
				{Name: "dest", Type: "string", Default: "yang/<device>", Format: collection.ParamFormatProjectPath, Description: "The project directory the sources are written into, a slash-separated path relative to the project, such as yang/core1; it defaults to yang/ and the device's name. Each file is name@revision.yang, or name.yang for a module the library names no revision of. An expression in it may read only vars."},
				{Name: "via", Type: "string", Default: collection.AlternativeAuto, Choices: []string{collection.AlternativeAuto, "netconf", "restconf"}, Description: "The protocol the sources are fetched over: auto (NETCONF when the device serves it, else RESTCONF), netconf or restconf. The device is held to the one named at plan time, and a failed fetch is never tried again over the other. gNMI has no way to fetch a source and is never one."},
				{Name: "max_modules", Type: "int", Default: "100", Description: "The most sources, modules and submodules together, one task fetches, at most 5000. A task that would fetch more is refused before any is fetched: name the modules, or raise this. A Catalyst 8000V's library lists 611."},
				{Name: "max_module_bytes", Type: "int", Default: "4194304", Description: "The largest one source may be, in bytes, at most 16777216 (16 MiB). A larger one is refused, never cut short."},
				{Name: "max_total_bytes", Type: "int", Default: "67108864", Description: "The most bytes of source one task fetches, at most 268435456 (256 MiB). A task past it is refused, and writes nothing."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Over NETCONF, skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, and the sources saved may then not be the device's, which the task warns; set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "via", Type: "string", Returned: "always", Description: "The protocol the sources were fetched over, netconf or restconf."},
				{Name: "dest", Type: "string", Returned: "always", Description: "The project directory the sources were written into."},
				{Name: "modules", Type: "list of dict", Returned: "always", Description: "One entry per source, in name order: name, revision, file (the path written, relative to the project), bytes, sha256 (of the file as written, in hex), submodule (true for a submodule), import_only (true for a module the device lists for its definitions only), masked (true when a known secret value was replaced in it) and changed (whether the file was written, or in a check would be)."},
				{Name: "files_changed", Type: "int", Returned: "always", Description: "How many files were written (in a check, would be) because their bytes differed or they were not there."},
				{Name: "bytes", Type: "int", Returned: "always", Description: "The sources' size together, in bytes, as written."},
			},
			Examples: []collection.Example{
				{Name: "Save the modules a configuration task needs", RunbookYAML: "- name: Read the schema of Tunnel100's models\n  net.yang.schema:\n    modules:\n      - Cisco-IOS-XE-native@2024-07-01\n      - Cisco-IOS-XE-tunnel\n      - Cisco-IOS-XE-crypto\n    dest: yang/core1\n"},
				{Name: "Save every module over RESTCONF", RunbookYAML: "- name: Read every module the router serves\n  net.yang.schema:\n    via: restconf\n    max_modules: 1000\n"},
			},
			SeeAlso: []string{"net.netconf.facts", "net.restconf.facts", "net.netconf.config", "net.restconf.config"},
		},
	},
}
