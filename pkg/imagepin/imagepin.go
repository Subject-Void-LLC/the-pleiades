// Package imagepin finds the container image references a build depends
// on (in Dockerfiles, Compose files and Kubernetes manifests), says which
// are not pinned by both tag and digest, and rewrites references in place.
// Each reference carries its exact byte range in the file, so a rewrite
// changes the reference and nothing else. build.digests is its caller.
package imagepin

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/distribution/reference"
)

// MaxFileBytes bounds a file the package reads.
const MaxFileBytes = 1 << 20

// Kind is what sort of file a reference was found in.
type Kind string

// The kinds of file read.
const (
	KindDockerfile Kind = "dockerfile"
	KindCompose    Kind = "compose"
	KindKubernetes Kind = "kubernetes"
)

// Ref is one image reference in a file.
type Ref struct {
	// Line is the 1-based line the reference's text is on.
	Line int
	// Start and End are the reference text's byte range in the file.
	Start, End int
	// Text is the reference as written.
	Text string
	// Where says what named it: "FROM", "COPY --from", "RUN --mount",
	// "ARG NAME" (a FROM through an ARG's default), or "image".
	Where string
	// Unresolvable says why the reference cannot be read as written (an
	// ARG with no default, a Compose variable); empty when it can.
	Unresolvable string
}

// Pin is how a reference is pinned.
type Pin struct {
	// Name is the repository as written (alpine, ghcr.io/x/y).
	Name string
	// Tag and Digest are empty when absent.
	Tag    string
	Digest string
}

// Pinned reports whether p names both a tag and a digest.
func (p Pin) Pinned() bool { return p.Tag != "" && p.Digest != "" }

// Problem is why a reference is not pinned, or "" when it is.
func (p Pin) Problem() string {
	switch {
	case p.Tag == "" && p.Digest == "":
		return "names neither a tag nor a digest"
	case p.Digest == "":
		return "names a tag but no digest, so what it runs can change underneath it"
	case p.Tag == "":
		return "names a digest but no tag, so nothing says what it is or what to move it forward to"
	}
	return ""
}

// Format writes p back as text: name, ":tag", "@digest".
func (p Pin) Format() string {
	s := p.Name
	if p.Tag != "" {
		s += ":" + p.Tag
	}
	if p.Digest != "" {
		s += "@" + p.Digest
	}
	return s
}

// digestPattern is a digest this package writes or accepts.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Parse reads a reference as written, keeping its own spelling of the
// name (no docker.io/library/ added), and refusing what no engine would
// pull.
func Parse(text string) (Pin, error) {
	if text == "" || strings.HasPrefix(text, "-") || strings.ContainsAny(text, " \t\n\r$`\\\"'") {
		return Pin{}, fmt.Errorf("%q is not an image reference", text)
	}
	if _, err := reference.ParseNormalizedNamed(text); err != nil {
		return Pin{}, fmt.Errorf("%q is not an image reference: %w", text, err)
	}
	var p Pin
	rest := text
	if name, digest, ok := strings.Cut(rest, "@"); ok {
		if !digestPattern.MatchString(digest) {
			return Pin{}, fmt.Errorf("%q names a digest that is not sha256:<64 hex>", text)
		}
		p.Digest, rest = digest, name
	}
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		p.Tag, rest = rest[i+1:], rest[:i]
	}
	p.Name = rest
	return p, nil
}

// Detect says what kind of file name and content are, or refuses a file of
// no kind this package reads.
func Detect(name string, content []byte) (Kind, error) {
	base := strings.ToLower(path.Base(name))
	switch {
	case dockerfileName.MatchString(base):
		return KindDockerfile, nil
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		return detectYAML(content)
	}
	return "", fmt.Errorf("%s is neither a Dockerfile or Containerfile nor a YAML file (Compose or Kubernetes)", name)
}

// dockerfileName matches a Dockerfile's or Containerfile's usual names.
var dockerfileName = regexp.MustCompile(`^((docker|container)file(\..+)?|.+\.(docker|container)file)$`)

// Extract finds every image reference in content, a file of kind.
func Extract(kind Kind, content []byte) ([]Ref, error) {
	if len(content) > MaxFileBytes {
		return nil, fmt.Errorf("the file is %d bytes, over the %d this reads", len(content), MaxFileBytes)
	}
	switch kind {
	case KindDockerfile:
		return extractDockerfile(content)
	case KindCompose:
		return extractCompose(content)
	case KindKubernetes:
		return extractKubernetes(content)
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// Edit replaces one reference's text.
type Edit struct {
	Ref  Ref
	Text string
}

// Rewrite applies edits to content, each replacing exactly its reference's
// byte range, and refuses edits that overlap or whose range no longer holds
// the text the reference was read as.
func Rewrite(content []byte, edits []Edit) ([]byte, error) {
	sorted := append([]Edit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ref.Start < sorted[j].Ref.Start })
	var out []byte
	at := 0
	for _, e := range sorted {
		r := e.Ref
		switch {
		case r.Start < at || r.End < r.Start || r.End > len(content):
			return nil, errors.New("edits overlap or fall outside the file")
		case string(content[r.Start:r.End]) != r.Text:
			return nil, fmt.Errorf("line %d no longer holds %q", r.Line, r.Text)
		}
		out = append(out, content[at:r.Start]...)
		out = append(out, e.Text...)
		at = r.End
	}
	return append(out, content[at:]...), nil
}

// lineOf is the 1-based line byte offset at falls on.
func lineOf(content []byte, at int) int {
	return strings.Count(string(content[:at]), "\n") + 1
}
