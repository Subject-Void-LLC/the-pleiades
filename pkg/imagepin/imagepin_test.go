// Tests for finding image references in build files, and rewriting them.
package imagepin

import (
	"strings"
	"testing"
)

// d is a digest for tests.
const d = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// refTexts is each located reference's text, or "!" and the text for an
// unresolvable one.
func refTexts(t *testing.T, refs []Ref, content string) []string {
	t.Helper()
	var out []string
	for _, r := range refs {
		if r.Unresolvable != "" {
			out = append(out, "!"+r.Text)
			continue
		}
		if content[r.Start:r.End] != r.Text {
			t.Errorf("ref %q at %d:%d holds %q", r.Text, r.Start, r.End, content[r.Start:r.End])
		}
		out = append(out, r.Text)
	}
	return out
}

// TestExtractDockerfile finds each kind of reference a Dockerfile can
// hold, and nothing that only looks like one.
func TestExtractDockerfile(t *testing.T) {
	for name, tc := range map[string]struct{ file, want string }{
		"plain and platform":                 {"FROM alpine:3.20\nFROM --platform=$BUILDPLATFORM golang:1.26@" + d + " AS build\n", "alpine:3.20 golang:1.26@" + d},
		"a stage and scratch":                {"FROM golang:1.26 AS Build\nFROM build\nFROM scratch\nCOPY --from=BUILD /x /x\nCOPY --from=0 /y /y\n", "golang:1.26"},
		"copy and mount from images":         {"FROM a:1\nCOPY --from=nginx:1.27 /etc/nginx /n\nRUN --mount=type=cache,target=/c --mount=type=bind,from=busybox:1.36,target=/b true\n", "a:1 nginx:1.27 busybox:1.36"},
		"an ARG default":                     {"ARG BASE=\"alpine:3.20\"\nFROM ${BASE}\nARG TOOL=busybox:1.36\nCOPY --from=$TOOL /bin/sh /sh\n", "alpine:3.20 busybox:1.36"},
		"an ARG with no default":             {"ARG BASE\nFROM $BASE\n", "!$BASE"},
		"part of it an ARG":                  {"ARG V=3.20\nFROM alpine:${V}\n", "!alpine:${V}"},
		"continuation, comments, case":       {"from \\\n  alpine:3.20 \\\n  as base\n# FROM ignored:1\nfrom base\n", "alpine:3.20"},
		"a heredoc body is not instructions": {"FROM a:1\nRUN <<EOF\nFROM inside:1\nEOF\nRUN <<-'END'\n\tFROM tabbed:1\n\tEND\nFROM b:2\n", "a:1 b:2"},
		"the escape directive":               {"# escape=`\nFROM alpine:3.20 `\n  AS x\nFROM x\n", "alpine:3.20"},
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := Extract(KindDockerfile, []byte(tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(refTexts(t, refs, tc.file), " "); got != tc.want {
				t.Errorf("refs = %s, want %s", got, tc.want)
			}
		})
	}
	if _, err := Extract(KindDockerfile, []byte("FROM --platform=linux/amd64\n")); err == nil {
		t.Error("a FROM naming no image was accepted")
	}
}

// TestExtractYAML finds Compose services' images and every container's
// image in every Kubernetes document, quoted or not.
func TestExtractYAML(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:1.27\n  db:\n    image: \"postgres:16@" + d + "\"\n  app:\n    image: ${APP_IMAGE:-app:1}\n  built:\n    build: .\n"
	refs, err := Extract(KindCompose, []byte(compose))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(refTexts(t, refs, compose), " "); got != "nginx:1.27 postgres:16@"+d+" !${APP_IMAGE:-app:1}" {
		t.Errorf("compose refs = %s", got)
	}

	k8s := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      initContainers:
        - name: init
          image: 'busybox:1.36'
      containers:
        - name: app
          image: ghcr.io/x/app:2.1
---
apiVersion: batch/v1
kind: CronJob
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - {name: job, image: alpine:3.20}
`
	refs, err = Extract(KindKubernetes, []byte(k8s))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(refTexts(t, refs, k8s), " "); got != "busybox:1.36 ghcr.io/x/app:2.1 alpine:3.20" {
		t.Errorf("kubernetes refs = %s", got)
	}
}

// TestDetect recognizes each kind by name and content, and refuses the
// rest by name.
func TestDetect(t *testing.T) {
	for name, want := range map[string]Kind{"Dockerfile": KindDockerfile, "build/Dockerfile.prod": KindDockerfile, "api.dockerfile": KindDockerfile, "Containerfile": KindDockerfile} {
		if got, err := Detect(name, nil); err != nil || got != want {
			t.Errorf("Detect(%s) = %s, %v", name, got, err)
		}
	}
	if got, _ := Detect("compose.yaml", []byte("services: {web: {image: a:1}}\n")); got != KindCompose {
		t.Errorf("compose = %s", got)
	}
	if got, _ := Detect("deploy.yml", []byte("apiVersion: v1\nkind: Pod\n")); got != KindKubernetes {
		t.Errorf("kubernetes = %s", got)
	}
	for name, content := range map[string]string{"notes.txt": "", "values.yaml": "replicas: 2\n", "bad.yaml": "{"} {
		if _, err := Detect(name, []byte(content)); err == nil {
			t.Errorf("Detect(%s) accepted it", name)
		}
	}
}

// TestParse reads each form a reference takes and says what is missing.
func TestParse(t *testing.T) {
	for text, want := range map[string]string{
		"alpine":                      "names neither a tag nor a digest",
		"alpine:3.20":                 "names a tag but no digest",
		"alpine@" + d:                 "names a digest but no tag",
		"alpine:3.20@" + d:            "",
		"localhost:5000/team/app:1.0": "names a tag but no digest",
		"registry:5000/app@" + d:      "names a digest but no tag",
	} {
		p, err := Parse(text)
		if err != nil {
			t.Errorf("Parse(%q): %v", text, err)
			continue
		}
		if p.Problem() != "" && !strings.HasPrefix(p.Problem(), want) || p.Problem() == "" && want != "" {
			t.Errorf("Parse(%q).Problem() = %q, want %q", text, p.Problem(), want)
		}
		if p.Format() != text {
			t.Errorf("Parse(%q).Format() = %q", text, p.Format())
		}
	}
	for _, bad := range []string{"", "-x", "a b", "$X", "Alpine:1", "a@sha256:short", "a;rm"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted it", bad)
		}
	}
}

// TestRewrite changes exactly the references, and refuses a file that no
// longer holds what was read.
func TestRewrite(t *testing.T) {
	file := "ARG BASE=alpine:3.20\nFROM ${BASE}\nCOPY --from=nginx:1.27 /a /a\n"
	refs, err := Extract(KindDockerfile, []byte(file))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rewrite([]byte(file), []Edit{{refs[0], "alpine:3.20@" + d}, {refs[1], "nginx:1.27@" + d}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "ARG BASE=alpine:3.20@" + d + "\nFROM ${BASE}\nCOPY --from=nginx:1.27@" + d + " /a /a\n"; string(out) != want {
		t.Errorf("rewrite =\n%s\nwant\n%s", out, want)
	}
	if _, err := Rewrite([]byte(strings.Replace(file, "nginx", "caddy", 1)), []Edit{{refs[1], "x:1"}}); err == nil {
		t.Error("a rewrite of text that changed was accepted")
	}
}

// FuzzExtract: any file of any kind either is refused or yields
// references whose located ranges hold exactly their text.
func FuzzExtract(f *testing.F) {
	f.Add("FROM alpine:3.20 AS a\nCOPY --from=a /x /x\n", "dockerfile")
	f.Add("services: {web: {image: 'a:1'}}\n", "compose")
	f.Add("apiVersion: v1\nkind: Pod\nspec: {containers: [{image: a:1}]}\n", "kubernetes")
	f.Fuzz(func(t *testing.T, content, kind string) {
		refs, err := Extract(Kind(kind), []byte(content))
		if err != nil {
			return
		}
		for _, r := range refs {
			if r.Unresolvable != "" {
				continue
			}
			if r.Start < 0 || r.End > len(content) || r.Start > r.End || content[r.Start:r.End] != r.Text {
				t.Fatalf("ref %+v does not hold its text in %q", r, content)
			}
		}
	})
}
