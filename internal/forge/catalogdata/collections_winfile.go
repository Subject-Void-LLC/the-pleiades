// This file holds the win.file.* Collection: files on a Windows host,
// reached over WinRM.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var winFileCollections = []collectionscaffold.Config{
	{
		Name:          "win.file.download",
		Capabilities:  []capability.Name{capability.NameWindowsShell},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Downloads a file onto a Windows host over HTTPS and keeps it only if its SHA-256 matches.",
			Description: "Makes sure path on the host holds the file url serves, verified by sha256. This is ansible.windows.win_get_url with a checksum it will not run without. A file already at path with that digest reports no change and nothing is downloaded. Otherwise the host fetches url with the curl.exe Windows ships, over HTTPS only, redirects included, into a file beside path, and moves it into place only once its digest matches; a mismatch leaves path as it was and fails, naming the digest it got. A file at path with another digest is replaced. The download is the host's own, so url must be reachable from it; nothing passes through the machine running Pleiades. A check reads path's digest and downloads nothing.",
			Params: []collection.Param{
				{Name: "url", Type: "string", Required: true, Description: "The https:// URL to fetch. It may not hold a quote, a space or a control character.", Format: collection.ParamFormatURL},
				{Name: "path", Type: "string", Required: true, Description: "The absolute path on the host to write, as G:\\iso\\ubuntu.ova. Its folder must exist. It may not hold a quote, a wildcard or a control character."},
				{Name: "sha256", Type: "string", Required: true, Description: "The file's SHA-256, 64 hex digits, as the publisher lists it (Ubuntu's SHA256SUMS, for one)."},
				{Name: "timeout", Type: "int", Default: "1800", Description: "How many seconds the download may take."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task made sure of."},
				{Name: "sha256", Type: "string", Returned: "always", Description: "Its SHA-256, lower case."},
				{Name: "size_bytes", Type: "int", Returned: "always", Description: "Its size."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The SHA-256 at path before this task and after it, empty where there was no file."},
			},
			Examples: []collection.Example{
				{Name: "Fetch an Ubuntu cloud image", RunbookYAML: "- name: Fetch the Ubuntu 24.04 cloud image\n  win.file.download:\n    url: https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-amd64.ova\n    path: G:\\PleiadesLab\\media\\ubuntu-24.04-server-cloudimg-amd64.ova\n    sha256: 513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.import_ova"},
		},
	},
}
