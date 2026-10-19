// This file holds win.archive.extract, an archive on a Windows host
// unpacked there over WinRM. The Doc literal is a copy of the method's own
// docs, kept equal by internal/archtest's TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var winArchiveCollections = []collectionscaffold.Config{
	{
		Name:          "win.archive.extract",
		Capabilities:  []capability.Name{capability.NameWindowsShell},
		Transports:    []string{"winrm"},
		Site:          collection.SiteTarget,
		Device:        collection.DeviceRequired,
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Extracts a .zip, .tar or .tar.gz already on a Windows host into a folder there, over WinRM.",
			Description: "Extracts src into dest, making dest when it does not exist, with the tar.exe Windows ships, which reads zip as well as tar and refuses an entry whose path is absolute or climbs out with \"..\". This is community.windows.win_unzip, and archive.extract for a Windows host: src must already be on the host. " +
				"Idempotency is opt-in, as archive.extract's: naming creates skips extraction when that path is already there; naming none means every run extracts again and reports a change. remove deletes src once it is extracted. " +
				"Every path is absolute and held to a pattern with no quote, wildcard or \"..\" before WinRM is reached. A check reads creates, src and dest and extracts nothing; a src not there yet, or a dest that is not a folder, makes the call unchecked rather than failed, since an earlier task may change either.",
			Params: []collection.Param{
				{Name: "src", Type: "string", Required: true, Description: "The archive on the host, an absolute path ending .zip, .tar, .tar.gz or .tgz."},
				{Name: "dest", Type: "string", Required: true, Description: "The folder to extract into, an absolute path or a drive's root (C:\\), made when it does not exist."},
				{Name: "creates", Type: "string", Description: "An absolute path whose existence means the extraction already happened; when it is there, nothing is extracted and the task reports no change."},
				{Name: "remove", Type: "bool", Default: "false", Description: "Delete src once it has been extracted."},
				{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds the extraction may take."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The folder extracted into."},
				{Name: "extracted", Type: "bool", Returned: "always", Description: "Whether this task extracted (or, in a check, would)."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether dest existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Unpack Go, once", RunbookYAML: "- name: Unpack Go into C:\\go\n  win.archive.extract:\n    src: C:\\Windows\\Temp\\go1.26.9.windows-amd64.zip\n    dest: C:\\\n    creates: C:\\go\\bin\\go.exe\n"},
			},
			SeeAlso: []string{"win.file.download", "archive.extract"},
		},
	},
}
