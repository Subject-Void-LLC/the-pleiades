// The container.image.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package image

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// filesNote is how an archive method reaches the engine machine's files.
const filesNote = " The archive is on the machine the engine runs on: this machine's own files when the engine is reached through its local socket, and otherwise the device's, over its SSH login."

// Shared parameters and returns.
var (
	pathParam   = collection.Param{Name: paramPath, Type: "string", Required: true, Description: "The archive's path on the machine the engine runs on: absolute and clean."}
	digestParam = collection.Param{Name: paramDigest, Type: "string", Description: "The archive's digest, sha256:<hex>, as container.image.save returned it. An archive with any other digest is refused before the engine reads a byte of it."}

	imageIDReturn = collection.ReturnField{Name: statImageID, Type: "string", Returned: "when the image is on the engine", Description: "The engine's ID for the image."}
	digestReturn  = collection.ReturnField{Name: statDigest, Type: "string", Returned: "always", Description: "The archive's digest, sha256:<hex>."}
	pathReturn    = collection.ReturnField{Name: statPath, Type: "string", Returned: "always", Description: "The archive's path."}
	sizeReturn    = collection.ReturnField{Name: statSize, Type: "int", Returned: "always", Description: "The archive's size in bytes."}
)

func pullDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Pulls an image by digest and names it by its tag.",
		Description: "Makes the image a reference names present on the engine. A tag is resolved through its registry to the digest it names now (a multi-platform image's index, so it means the same on every machine), the image is pulled by that digest when absent, and the tag is then pointed at it on the engine; an image named by digest is pulled as named. The registry credential bound for the image's registry is used when there is one. Present and tagged is no change. A run that moved an existing tag records moving it back as the undo, guarded by the image it names now; when the run also pulled, that undo is partial, since the pulled image stays for container.image.prune. A run that pulled an absent image under a new tag records its removal (nothing can be using an image that was not there), and one that added a tag to an image already there records removing the tag, unless it is the image's only name. A pull whose tagging then fails removes the image it pulled. A check resolves the digest and reads the engine, and pulls and tags nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImage, Type: "string", Required: true, Description: "The image: a reference by tag (resolved to its digest), or by digest."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImage, Type: "string", Returned: "always", Description: "The image by digest."},
			{Name: statDigest, Type: "string", Returned: "always", Description: "The image's digest."},
			imageIDReturn,
			{Name: statPulled, Type: "bool", Returned: "always", Description: "Whether the image had to be pulled (by a check, would be)."},
			{Name: statTagged, Type: "bool", Returned: "always", Description: "Whether the tag had to be pointed at the image (by a check, would be)."},
		},
		Examples: []collection.Example{{Name: "Pull a pinned web server", RunbookYAML: "- name: Pull nginx\n  container.image.pull:\n    image: nginx:1.28.1-alpine\n"}},
		SeeAlso:  []string{"container.image.resolve", "container.image.remove", "container.run"},
	}
}

func pushDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Pushes an image to its registry.",
		Description: "Pushes the local image a tag names to the registry the tag names, with the registry credential bound for that registry when there is one. The registry is asked first what the tag names there; when that is already the local image, nothing is sent and there is no change. A registry that cannot say (a repository not yet created) is pushed to. A push has no undo: the engine has no operation that deletes from a registry. A check reads the image and asks the registry, and sends nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImage, Type: "string", Required: true, Description: "The image by tag, registry included (ghcr.io/org/app:1.2). A reference by digest is refused: a registry is pushed to by tag."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImage, Type: "string", Returned: "always", Description: "The image's reference."},
			imageIDReturn,
			{Name: statDigest, Type: "string", Returned: "when the registry holds the image", Description: "The digest the registry's tag names, after the push."},
		},
		Examples: []collection.Example{{Name: "Push a release", RunbookYAML: "- name: Publish\n  container.image.push:\n    image: registry.example.com/app:1.4.2\n"}},
		SeeAlso:  []string{"container.image.tag", "container.registry.login"},
	}
}

func tagDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Names an image by another tag.",
		Description: "Points tag at the image source names (a reference or an image ID). A tag already naming that image is no change; one naming another image is moved. A missing source is refused. A run that moved the tag records moving it back as the undo, guarded by the image it names now, and one that added a new tag records removing it, unless the tag is the image's only name, since removing that deletes the image. A check reads both and tags nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramSource, Type: "string", Required: true, Description: "The image to name: a reference or an image ID."},
			{Name: paramTarget, Type: "string", Required: true, Description: "The tag to point at it, repository:tag; a missing tag is latest. A reference by digest is refused."},
			{Name: paramID, Type: "string", Description: "The image the tag must name now, by ID; when it names another, or none, the task is refused and nothing is tagged. The undo of a run that moved the tag sets it, so a rollback never moves a tag someone has moved again since."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImage, Type: "string", Returned: "always", Description: "The tag."},
			imageIDReturn,
		},
		Examples: []collection.Example{{Name: "Name a build for its registry", RunbookYAML: "- name: Tag for the registry\n  container.image.tag:\n    source: app:build-118\n    tag: registry.example.com/app:1.4.2\n"}},
		SeeAlso:  []string{"container.image.push", "container.image.remove"},
	}
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes an image or one of its tags.",
		Description: "Removes the reference image names: a tag is removed, and the image with it when it was the image's last name; an image ID removes the image and every name it has. An absent image is no change. An image a container uses, in any state, is refused unless force is set. With id, a reference that now names another image is refused, so an undo never removes an image that took the tag since. A removal has no undo. A check reads the image and the containers, and removes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImage, Type: "string", Required: true, Description: "The image: a reference, or an image ID."},
			{Name: paramID, Type: "string", Description: "The image ID the reference must still name."},
			{Name: paramForce, Type: "bool", Default: "false", Description: "Remove an image a stopped container uses; the engine still refuses one a running container uses. On Podman, force also deletes the stopped containers using the image, which Docker leaves."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImage, Type: "string", Returned: "always", Description: "The reference the task named."},
			imageIDReturn,
			{Name: statUsedBy, Type: "list of string", Returned: "when the image exists", Description: "The containers using the image, by name."},
			{Name: statRemoved, Type: "list of string", Returned: "when it was removed", Description: "The tags the engine removed and the image IDs it deleted."},
		},
		Examples: []collection.Example{{Name: "Remove an old build", RunbookYAML: "- name: Remove build 117\n  container.image.remove:\n    image: app:build-117\n"}},
		SeeAlso:  []string{"container.image.prune", "container.remove"},
	}
}

func infoDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reports images on the engine.",
		Description: "Reports each image named, or every image the engine lists when none is named (an untagged one with no tags): ID, tags, repository digests, creation time, size and labels, and for a named image its OS and architecture. A named image that is absent is listed under missing rather than failing the task. An image's environment is never reported, since a value baked into an image can be a secret. It changes nothing; a check is the same read." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImages, Type: "list", Description: "The images to report, by reference or ID. Absent means every image."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImages, Type: "list of dict", Returned: "always", Description: "One entry per image: id, tags, digests, created, size, labels, and os and architecture for an image named."},
			{Name: statMissing, Type: "list of string", Returned: "always", Description: "The images named that the engine does not have."},
		},
		Examples: []collection.Example{{Name: "Is the release here", RunbookYAML: "- name: Read the release image\n  container.image.info:\n    images: [registry.example.com/app:1.4.2]\n  register: release\n"}},
		SeeAlso:  []string{"container.image.pull", "container.image.resolve"},
	}
}

func resolveDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Resolves a tag to the digest it names in its registry.",
		Description: "Asks the registry a reference names, through the engine (so through the device's own network, proxy and mirrors), which digest its tag names now: a multi-platform image's index, as `docker buildx imagetools inspect` reports it, so it means the same on any machine. The registry credential bound for that registry is used when there is one. It changes nothing; a check is the same read." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImage, Type: "string", Required: true, Description: "The image by tag; a missing tag is latest."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statImage, Type: "string", Returned: "always", Description: "The image by digest."},
			{Name: statDigest, Type: "string", Returned: "always", Description: "The digest the tag names."},
			{Name: statMediaType, Type: "string", Returned: "always", Description: "Whether the digest is an index or a single image's manifest, as its media type."},
			{Name: statPlatform, Type: "list of string", Returned: "always", Description: "The platforms the image is built for, os/arch[/variant]."},
		},
		Examples: []collection.Example{{Name: "Pin a base image", RunbookYAML: "- name: Resolve the base\n  container.image.resolve:\n    image: alpine:3.20\n  register: base\n"}},
		SeeAlso:  []string{"container.image.pull", "build.digests.refresh"},
	}
}

func searchDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Searches a registry for images.",
		Description: "Runs the registry's search through the engine, anonymously: Docker Hub unless term names another registry. Each result's name, description, stars and whether it is official are returned, with control characters dropped from the registry's text. It changes nothing; a check is the same read." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramTerm, Type: "string", Required: true, Description: "What to search for."},
			{Name: paramLimit, Type: "int", Default: "25", Description: "The most results to return, from 1 to 100."},
			{Name: paramOfficial, Type: "bool", Default: "false", Description: "Return official images only."},
			{Name: paramStars, Type: "int", Default: "0", Description: "Return images with at least this many stars only."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statResults, Type: "list of dict", Returned: "always", Description: "One entry per image found: name, description, stars, official."},
		},
		Examples: []collection.Example{{Name: "Find an official image", RunbookYAML: "- name: Search\n  container.image.search:\n    term: postgres\n    official_only: true\n    limit: 5\n"}},
		SeeAlso:  []string{"container.image.pull"},
	}
}

func saveDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Saves images to an archive and returns its digest.",
		Description: "Writes the images named, with their tags, as one archive at path, and returns the archive's digest, which container.image.load takes to refuse any other archive: one build's archive serves every later task. The archive is written to a private temporary (mode 0600) beside path and renamed over it, so a reader sees the old file or the new one; a file already holding the same archive is left as it is and is no change. A missing image is refused. The archive replaces what was at path, which is not kept, so a save has no undo. A check reads the images and streams the archive through a hash, writing nothing, so it predicts the digest exactly." + filesNote + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramImages, Type: "list", Required: true, Description: "The images to save, by reference or ID."},
			pathParam,
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			pathReturn, digestReturn, sizeReturn,
			{Name: statImages, Type: "list of string", Returned: "always", Description: "The saved images' IDs."},
		},
		Examples: []collection.Example{{Name: "Save a build for later tasks", RunbookYAML: "- name: Save the build\n  container.image.save:\n    images: [app:build-118]\n    path: /var/lib/builds/app-118.tar\n  register: archive\n"}},
		SeeAlso:  []string{"container.image.load", "file.remove"},
	}
}

func loadDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Loads images from an archive, refusing one whose digest differs.",
		Description: "Loads the archive at path, as container.image.save or docker save wrote it, into the engine. With digest, an archive with any other digest is refused before the engine reads it: the archive is copied to a private temporary file on the machine running the task, hashed on the way, and the engine loads that copy, so what it loads is what was checked. Loading always reports a change, and has no undo, since a load can move tags from the images they named and the engine does not say which. A check reads and hashes the archive, and loads nothing." + filesNote + containerengine.ReachNote,
		Params:      []collection.Param{pathParam, digestParam, containerengine.HostKeyParam},
		Fragments:   []string{"container_engine"},
		Returns: []collection.ReturnField{
			pathReturn, digestReturn, sizeReturn,
			{Name: statImages, Type: "list of string", Returned: "when it ran", Description: "The images the engine said it loaded, by tag or ID."},
		},
		Examples: []collection.Example{{Name: "Load the saved build", RunbookYAML: "- name: Save the build\n  container.image.save:\n    images: [app:build-118]\n    path: /var/lib/builds/app-118.tar\n  register: archive\n\n- name: Load the build\n  container.image.load:\n    path: /var/lib/builds/app-118.tar\n    digest: \"{{ result.archive.digest }}\"\n"}},
		SeeAlso:  []string{"container.image.save", "container.image.import"},
	}
}

func importDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes an image from a filesystem archive.",
		Description: "Imports the filesystem archive at path (a tar of a root filesystem, as container export writes it) as a one-layer image the tag names. With digest, an archive with any other digest is refused before the engine reads it, as for container.image.load. An import makes a new image each time, so it always reports a change. A run records pointing the tag back at the image it named before as the undo, guarded by the image it names now, and partial, since the imported image stays for container.image.prune; or removing the imported image when the tag was new. An import that fails after reaching the engine puts the tag back, or removes what it made, since a failed task records no undo. A check reads and hashes the archive, and imports nothing." + filesNote + containerengine.ReachNote,
		Params: []collection.Param{
			pathParam,
			{Name: paramImage, Type: "string", Required: true, Description: "The tag the new image gets, repository:tag; a missing tag is latest."},
			digestParam,
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			pathReturn, digestReturn, sizeReturn,
			{Name: statImage, Type: "string", Returned: "always", Description: "The tag the image gets."},
			{Name: statImageID, Type: "string", Returned: "when it ran", Description: "The new image's ID."},
		},
		Examples: []collection.Example{{Name: "Import a root filesystem", RunbookYAML: "- name: Import the rootfs\n  container.image.import:\n    path: /srv/rootfs/base.tar\n    image: base:2026-10\n"}},
		SeeAlso:  []string{"container.image.load"},
	}
}

func pruneDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Keeps each repository's newest tags and removes the rest.",
		Description: "For each repository on the engine whose name matches repositories, orders its tags as versions, as `sort -V` does (1.10 after 1.9; a word such as latest after every number), keeps the keep_newest newest and removes the rest. A tag of an image a container uses, in any state, is never removed, and does not count toward keep_newest. Removing a tag removes the image only when it was the image's last name. Nothing is forced, so a container that starts using an image meanwhile stops the prune there. A prune has no undo. A check reads the images and containers and removes nothing, returning what a run would remove." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramRepositories, Type: "string", Required: true, Description: "A pattern over repository names as the engine shows them (app, ghcr.io/org/app): * matches any run of characters but /, ? one character, [...] a set."},
			{Name: paramKeepNewest, Type: "int", Required: true, Description: "How many of each repository's newest tags to keep, from 0 to 1000."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statKept, Type: "list of string", Returned: "always", Description: "The tags kept as the newest."},
			{Name: statKeptInUse, Type: "list of string", Returned: "always", Description: "The older tags kept because a container uses their image."},
			{Name: statRemoved, Type: "list of string", Returned: "always", Description: "The tags removed (by a check, that would be)."},
		},
		Examples: []collection.Example{{Name: "Keep the last three builds", RunbookYAML: "- name: Prune old builds\n  container.image.prune:\n    repositories: registry.example.com/app\n    keep_newest: 3\n"}},
		SeeAlso:  []string{"container.image.remove"},
	}
}
