// Tests for the shapes a first reading of build files got wrong: a word
// that only contains "<<", an ARG's scope, one ARG used twice, and YAML's
// merge keys, aliases, anchors and duplicate keys.
package imagepin

import (
	"strings"
	"testing"
)

// TestDockerfile_HeredocsAreBuildKits: a heredoc opens only where
// BuildKit opens one, a word starting with "<<" in RUN, COPY or ADD, so
// an arithmetic shift or a quoted "<<" cannot hide the FROMs after it.
func TestDockerfile_HeredocsAreBuildKits(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want []string
	}{
		"a shift in RUN":          {"FROM a:1\nRUN echo $((1<<X))\nFROM b:2\n", []string{"a:1", "b:2"}},
		"a quoted << in a LABEL":  {"FROM a:1\nLABEL x=\"a<<EOF\"\nFROM b:2\n", []string{"a:1", "b:2"}},
		"a real heredoc":          {"FROM a:1\nRUN <<EOF\nFROM hidden:1\nEOF\nFROM b:2\n", []string{"a:1", "b:2"}},
		"a heredoc after a word":  {"FROM a:1\nRUN cat <<'EOF' > f\nFROM hidden:1\nEOF\nFROM b:2\n", []string{"a:1", "b:2"}},
		"a tab-stripped heredoc":  {"FROM a:1\nRUN <<-EOT\n\tFROM hidden:1\n\tEOT\nFROM b:2\n", []string{"a:1", "b:2"}},
		"a heredoc with a fd":     {"FROM a:1\nRUN 3<<EOF cat <&3\nFROM hidden:1\nEOF\nFROM b:2\n", []string{"a:1", "b:2"}},
		"<< in an ENV is no file": {"FROM a:1\nENV X=<<EOF\nFROM b:2\n", []string{"a:1", "b:2"}},
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := Extract(KindDockerfile, []byte(tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got := refTexts(t, refs, tc.file); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("refs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDockerfile_ARGScope: a FROM reads only the ARGs declared before the
// first FROM; an ARG inside a stage is that stage's alone, and a bare one
// there imports the global value into the stage without changing it.
func TestDockerfile_ARGScope(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want []string
	}{
		"a stage ARG does not change a later FROM": {
			"ARG BASE=evil:latest\nFROM a:1\nARG BASE=good:1@" + d + "\nFROM ${BASE}\n", []string{"a:1", "evil:latest"},
		},
		"a bare stage ARG leaves the global": {
			"ARG BASE=b:2\nFROM a:1\nARG BASE\nFROM $BASE\n", []string{"a:1", "b:2"},
		},
		"a stage ARG is what COPY --from reads": {
			"FROM a:1\nARG SRC=c:3\nCOPY --from=$SRC /x /x\n", []string{"a:1", "c:3"},
		},
		"a bare stage ARG imports the global for COPY --from": {
			"ARG SRC=c:3\nFROM a:1\nARG SRC\nCOPY --from=${SRC} /x /x\n", []string{"a:1", "c:3"},
		},
		"a global ARG is not in a stage until imported": {
			"ARG SRC=c:3\nFROM a:1\nCOPY --from=${SRC} /x /x\n", []string{"a:1", "!${SRC}"},
		},
		"a stage's ARGs end with it": {
			"FROM a:1\nARG SRC=c:3\nFROM b:2\nCOPY --from=$SRC /x /x\n", []string{"a:1", "b:2", "!$SRC"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			refs, err := Extract(KindDockerfile, []byte(tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got := refTexts(t, refs, tc.file); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("refs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDockerfile_OneARGUsedTwiceIsOneReference: two FROMs through one ARG
// name one place to pin, and rewriting it is one edit, not two that
// overlap.
func TestDockerfile_OneARGUsedTwiceIsOneReference(t *testing.T) {
	file := "ARG BASE=alpine:3.20\nFROM ${BASE} AS one\nFROM ${BASE} AS two\n"
	refs, err := Extract(KindDockerfile, []byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if got := refTexts(t, refs, file); strings.Join(got, ",") != "alpine:3.20" {
		t.Fatalf("refs = %v, want the ARG's default once", got)
	}
	out, err := Rewrite([]byte(file), []Edit{{refs[0], "alpine:3.20@" + d}})
	if err != nil || !strings.HasPrefix(string(out), "ARG BASE=alpine:3.20@"+d+"\n") {
		t.Fatalf("Rewrite = %q, %v", out, err)
	}
}

// TestYAML_MergesAliasesAnchors: an image reached through a merge key or
// an alias is found, at the anchor that holds its text, once; an anchored
// value is located after its anchor, never inside it.
func TestYAML_MergesAliasesAnchors(t *testing.T) {
	compose := "x-base: &base\n  image: &img \"alpine:3.20\"\nservices:\n  web:\n    <<: *base\n  api:\n    image: *img\n  db:\n    <<: [*base]\n    image: postgres:16\n"
	refs, err := Extract(KindCompose, []byte(compose))
	if err != nil {
		t.Fatal(err)
	}
	if got := refTexts(t, refs, compose); strings.Join(got, ",") != "alpine:3.20,postgres:16" {
		t.Errorf("compose refs = %v, want the anchored image once and db's own", got)
	}

	sameName := "services:\n  web:\n    image: &alpine alpine\n"
	refs, err = Extract(KindCompose, []byte(sameName))
	if err != nil || len(refs) != 1 || refs[0].Unresolvable != "" || sameName[refs[0].Start:refs[0].End] != "alpine" || refs[0].Start != strings.LastIndex(sameName, "alpine") {
		t.Errorf("an anchor named like its value: %+v, %v", refs, err)
	}

	k8s := "apiVersion: v1\nkind: Pod\nmetadata: {name: p}\nspec:\n  containers: &cs\n    - name: a\n      image: a:1\n  initContainers: *cs\n"
	refs, err = Extract(KindKubernetes, []byte(k8s))
	if err != nil {
		t.Fatal(err)
	}
	if got := refTexts(t, refs, k8s); strings.Join(got, ",") != "a:1" {
		t.Errorf("kubernetes refs = %v, want a:1 once", got)
	}
}

// TestYAML_RefusesDuplicateKeys: a key given twice in one mapping is
// refused, since readers keep different ones (Compose and kubectl's strict
// decoding refuse it too), so the image checked could be the one not run.
func TestYAML_RefusesDuplicateKeys(t *testing.T) {
	for kind, file := range map[Kind]string{
		KindCompose:    "services:\n  web:\n    image: a:1@" + d + "\n    image: evil:latest\n",
		KindKubernetes: "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n    - name: a\n      image: a:1@" + d + "\n      image: evil:latest\n",
	} {
		if _, err := Extract(kind, []byte(file)); err == nil || !strings.Contains(err.Error(), `"image" is given twice`) {
			t.Errorf("%s with a duplicate key: %v", kind, err)
		}
		if _, err := Detect("f.yaml", []byte(file)); err == nil {
			t.Errorf("Detect read a %s with a duplicate key", kind)
		}
	}
}
