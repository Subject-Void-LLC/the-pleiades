// Command release builds the binaries a Pleiades release ships, for every
// platform it ships on, and writes them as one archive per platform beside a
// SHA256SUMS file. make release runs it, and so does the tag workflow, so
// there is no CI-only step.
//
// Every binary is built the way binsize measures one (static, stripped,
// trimmed paths) and stamped through internal/buildinfo's -ldflags, so the
// version it reports is the release it was cut for. The command builds and
// writes files; it never publishes anything. With -notes it builds nothing and
// instead prepares the release page's body from the tag's notes file (see
// notes.go). Signing, an SBOM and provenance are Phase 106c's.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// buildinfoPath is the import path whose version and revision variables the
// linker stamps.
const buildinfoPath = "github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"

// DefaultPlatforms are the GOOS/GOARCH pairs a release ships: three
// operating systems on both architectures.
const DefaultPlatforms = "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64"

// DefaultBinaries are the programs a release ships, each a package under
// ./cmd.
const DefaultBinaries = "pleiades,controller,runner"

// TitleFile is the name of the file holding the release's title, v0.3.0 and its
// codename, which the tag workflow reads and does not upload.
const TitleFile = "RELEASE_TITLE"

// ChecksumFile is the name of the checksum list written beside the archives.
const ChecksumFile = "SHA256SUMS"

// Platform is one operating system and architecture a release is built for.
type Platform struct {
	// OS is the GOOS value.
	OS string
	// Arch is the GOARCH value.
	Arch string
}

// String returns the platform as GOOS/GOARCH.
func (p Platform) String() string { return p.OS + "/" + p.Arch }

// archiveName returns the file name of the archive holding every binary built
// for p at version v: a zip on Windows, where it is the usual form, and a
// gzipped tar elsewhere.
func archiveName(v string, p Platform) string {
	ext := ".tar.gz"
	if p.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("the-pleiades_%s_%s_%s%s", v, p.OS, p.Arch, ext)
}

// binaryName returns the file name of the program name on p.
func binaryName(name string, p Platform) string {
	if p.OS == "windows" {
		return name + ".exe"
	}
	return name
}

// parsePlatforms reads a comma-separated list of GOOS/GOARCH pairs.
func parsePlatforms(s string) ([]Platform, error) {
	var out []Platform
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		goos, arch, ok := strings.Cut(f, "/")
		if !ok || goos == "" || arch == "" || strings.Contains(arch, "/") {
			return nil, fmt.Errorf("platform %q is not GOOS/GOARCH", f)
		}
		if seen[f] {
			return nil, fmt.Errorf("platform %q is listed twice", f)
		}
		seen[f] = true
		out = append(out, Platform{OS: goos, Arch: arch})
	}
	if len(out) == 0 {
		return nil, errors.New("no platforms given")
	}
	return out, nil
}

// parseBinaries reads a comma-separated list of program names, each a
// directory name under ./cmd, and refuses anything that could leave it.
func parseBinaries(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.ContainsAny(f, `/\.`) {
			return nil, fmt.Errorf("binary %q is not a plain ./cmd directory name", f)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, errors.New("no binaries given")
	}
	return out, nil
}

// ldflags returns the linker flags of a release build: stripped, with the
// version and revision stamped into internal/buildinfo.
func ldflags(version, revision string) string {
	return fmt.Sprintf("-s -w -X %s.version=%s -X %s.revision=%s", buildinfoPath, version, buildinfoPath, revision)
}

// build compiles ./cmd/<name> for p into dir and returns the file written.
// Cgo is off, as a static release binary is built.
func build(name string, p Platform, version, revision, dir string) (string, error) {
	out := filepath.Join(dir, binaryName(name, p))
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags(version, revision), "-o", out, "./cmd/"+name) // #nosec G204 -- fixed flags; name is checked by parseBinaries and the platform by parsePlatforms
	cmd.Env = append(os.Environ(), "GOOS="+p.OS, "GOARCH="+p.Arch, "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s for %s: %v\n%s", name, p, err, msg)
	}
	return out, nil
}

// member is one file going into an archive.
type member struct {
	// name is the path inside the archive.
	name string
	// path is the file on disk.
	path string
	// mode is the permission bits recorded.
	mode os.FileMode
}

// writeArchive writes members to dst as a zip when dst ends in .zip and as a
// gzipped tar otherwise. Times are zero and members are written in the order
// given, so the same inputs make the same archive.
func writeArchive(dst string, members []member) (err error) {
	f, err := os.Create(dst) // #nosec G304 -- dst is under the caller's own output directory
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if strings.HasSuffix(dst, ".zip") {
		return writeZip(f, members)
	}
	return writeTarGz(f, members)
}

// writeZip writes members as a zip to w.
func writeZip(w io.Writer, members []member) error {
	zw := zip.NewWriter(w)
	for _, m := range members {
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		h.SetMode(m.mode)
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if err := copyFile(fw, m.path); err != nil {
			return err
		}
	}
	return zw.Close()
}

// writeTarGz writes members as a gzipped tar to w.
func writeTarGz(w io.Writer, members []member) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		info, err := os.Stat(m.path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: int64(m.mode.Perm()), Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if err := copyFile(tw, m.path); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// copyFile copies the file at path to w.
func copyFile(w io.Writer, path string) error {
	src, err := os.Open(path) // #nosec G304 -- a file this command just built or the repository's own LICENSE
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = io.Copy(w, src)
	return err
}

// checksum returns the SHA-256 of the file at path as lowercase hex.
func checksum(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- an archive this command just wrote
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeChecksums writes the SHA256SUMS file for the archives named, in the
// format sha256sum -c reads: the digest, two spaces, the file name.
func writeChecksums(dir string, archives []string) error {
	sort.Strings(archives)
	var b strings.Builder
	for _, a := range archives {
		sum, err := checksum(filepath.Join(dir, a))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, a)
	}
	return os.WriteFile(filepath.Join(dir, ChecksumFile), []byte(b.String()), 0o644) // #nosec G306 -- a public checksum list
}

// main runs the command and exits with its status.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the command: 0 when every archive and the checksum list were
// written, 1 when a build or a write failed, 2 for a usage error.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "the release to build, MAJOR.MINOR.PATCH with an optional prerelease (required)")
	platforms := fs.String("platforms", DefaultPlatforms, "comma-separated GOOS/GOARCH pairs to build for")
	binaries := fs.String("binaries", DefaultBinaries, "comma-separated programs under ./cmd to build")
	out := fs.String("out", "dist", "the directory the archives and "+ChecksumFile+" are written to")
	revision := fs.String("revision", "", "the commit stamped into the build (default: HEAD, read with git)")
	notes := fs.String("notes", "", "write this release notes file, links made absolute for the release page, as "+NotesFile+" in -out, and build nothing (needs -repo)")
	repo := fs.String("repo", "", "the GitHub repository OWNER/NAME the notes' links point into (with -notes)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "release: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	v := strings.TrimPrefix(*version, "v")
	if _, ok := buildinfo.Release(v); !ok {
		fmt.Fprintf(stderr, "release: -version %q is not a release such as 0.3.0\n", *version)
		return 2
	}
	// Notes mode prepares the release page's body and builds nothing.
	if *notes != "" {
		return runNotes(*notes, *repo, v, *out, stdout, stderr)
	}
	plats, err := parsePlatforms(*platforms)
	if err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 2
	}
	bins, err := parseBinaries(*binaries)
	if err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 2
	}
	rev := *revision
	if rev == "" {
		b, err := exec.Command("git", "rev-parse", "HEAD").Output()
		if err != nil {
			fmt.Fprintf(stderr, "release: read HEAD: %v\n", err)
			return 1
		}
		rev = strings.TrimSpace(string(b))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil { // #nosec G301 -- release output is meant to be read by anyone
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	work, err := os.MkdirTemp("", "release-")
	if err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	defer os.RemoveAll(work)

	var archives []string
	for _, p := range plats {
		dir := filepath.Join(work, p.OS+"_"+p.Arch)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintf(stderr, "release: %v\n", err)
			return 1
		}
		members := []member{{name: "LICENSE", path: "LICENSE", mode: 0o644}}
		for _, b := range bins {
			path, err := build(b, p, v, rev, dir)
			if err != nil {
				fmt.Fprintf(stderr, "release: %v\n", err)
				return 1
			}
			members = append(members, member{name: binaryName(b, p), path: path, mode: 0o755})
		}
		name := archiveName(v, p)
		if err := writeArchive(filepath.Join(*out, name), members); err != nil {
			fmt.Fprintf(stderr, "release: write %s: %v\n", name, err)
			return 1
		}
		archives = append(archives, name)
		fmt.Fprintf(stdout, "built %s\n", name)
	}
	title := "v" + v
	if name := buildinfo.CodenameFor(v); name != "" {
		title += " \"" + name + "\""
	}
	if err := os.WriteFile(filepath.Join(*out, TitleFile), []byte(title+"\n"), 0o644); err != nil { // #nosec G306 -- a public title
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	if err := writeChecksums(*out, archives); err != nil {
		fmt.Fprintf(stderr, "release: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s for %d archives in %s; nothing was published\n", ChecksumFile, len(archives), *out)
	return 0
}
