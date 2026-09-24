//go:build integration

// Differential test: YAML merge keys as the converter reads them against
// ansible-core's own loader, in the repository's Ansible runner image.
package playbook

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// mergeDocuments exercise every precedence rule: an explicit key over a
// merged one wherever it is written, an earlier map in a merge list over a
// later one, a later merge key over an earlier one, and a merge of a map
// that itself merges.
var mergeDocuments = []string{
	"a: &a {x: 1, y: 1}\nb: &b {y: 2, z: 2}\nm: {<<: [*a, *b], x: 0}\n",
	"a: &a {x: 1, y: 1}\nb: &b {y: 2, z: 2}\nm:\n  <<: *a\n  <<: *b\n",
	"a: &a {x: 1, y: 1}\nm: {x: 0, <<: *a}\n",
	"a: &a {x: 1}\nc: &c {<<: *a, y: 3}\nm: {<<: *c, z: 4}\n",
	"a: &a {x: 1, y: 1}\nb: &b {x: 2}\nc: &c {y: 3}\nm: {<<: [*b, *c, *a]}\n",
}

// TestMergeKeys_MatchAnsible reads each document as the converter does
// and as ansible-core's AnsibleLoader does, and requires the same values.
func TestMergeKeys_MatchAnsible(t *testing.T) {
	image := testsupport.BuildAnsibleRunnerImage(t)
	const loader = `import json, sys
from ansible.parsing.yaml.loader import AnsibleLoader
print(json.dumps([AnsibleLoader(doc).get_single_data() for doc in json.load(sys.stdin)]))`
	input, err := json.Marshal(mergeDocuments)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "-i", image, "python3", "-c", loader)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("AnsibleLoader: %v", err)
	}
	var ansible []any
	if err := json.Unmarshal(out, &ansible); err != nil {
		t.Fatalf("AnsibleLoader output: %v\n%s", err, out)
	}
	for i, doc := range mergeDocuments {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
			t.Fatal(err)
		}
		ours, err := naturalValue(&root)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		// Through JSON, so both sides hold numbers the same way.
		encoded, _ := json.Marshal(ours)
		var normalized any
		_ = json.Unmarshal(encoded, &normalized)
		if !reflect.DeepEqual(normalized, ansible[i]) {
			t.Errorf("%q\nconverter: %v\nAnsible:   %v", doc, normalized, ansible[i])
		}
	}
}
