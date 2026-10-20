// Gate for examples/ai_repair_lab through the real binary, against this
// machine's real Docker engine and a real HTTP server speaking Ollama's API:
// the example's devices onboard, its runbook validates and checks without a
// model call, a disk_full run is rescued by the fix the model names, and a
// rollback of it removes the container through container.run's recorded undo
// while reporting every rollback: [] task as nothing to undo.
//
// The model is a stand-in that answers what it is told to, so this proves
// the platform routes a model's pick to the right fixed task, never that a
// real model picks well; the example's README records the runs against a
// real gemma4.
package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// aiRepairExampleImage is the image the example's runbook runs. Named here,
// tools/testimages lists it, so CI holds it before this test runs.
const aiRepairExampleImage = "busybox:1.36"

// aiRepairExample copies examples/ai_repair_lab into a temporary project
// whose model host is host, whose container is name, and whose image is
// run as the engine holds it (pull: never, as on a host with no registry),
// and onboards both devices.
func aiRepairExample(t *testing.T, host *aitest.Ollama, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "ai_repair_lab", "pleiades"))); err != nil {
		t.Fatal(err)
	}
	edit := func(path string, r *strings.Replacer) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(r.Replace(string(data))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit(filepath.Join(dir, "inventory.yaml"), strings.NewReplacer("http://127.0.0.1:11434", host.URL))
	runbook := filepath.Join(dir, "runbooks", "repair_web.yaml")
	data, err := os.ReadFile(runbook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "image: "+aiRepairExampleImage+"\n") {
		t.Fatalf("the example no longer runs %s; change aiRepairExampleImage with it", aiRepairExampleImage)
	}
	edit(runbook, strings.NewReplacer("name: ai-repair-web\n", "name: "+name+"\n",
		"image: "+aiRepairExampleImage+"\n", "image: "+aiRepairExampleImage+"\n          pull: never\n"))
	env := personEnv(nil)
	for _, device := range []string{"dockerhost", "workstation"} {
		if out, err := runPleiadesWithEnv(t, dir, env, "onboard", device); err != nil {
			t.Fatalf("onboard %s: %v\n%s", device, err, out)
		}
	}
	return dir
}

// TestCLI_AIRepairExampleRepairsAndRollsBack is the gate.
func TestCLI_AIRepairExampleRepairsAndRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real container on this machine's Docker engine")
	}
	_, err := os.Stat("/var/run/docker.sock")
	testsupport.Require(t, "docker", err == nil, "onboarding the example's container host reads this machine's engine socket")
	testsupport.Require(t, aiRepairExampleImage, exec.Command("docker", "image", "inspect", aiRepairExampleImage).Run() == nil,
		"the example's image is run as the engine holds it; pull it, or run tools/testimages -pull")

	name := "ai-repair-gate-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	host := aitest.NewOllama(t, "gemma4:e4b-it-qat")
	dir := aiRepairExample(t, host, name)
	env := personEnv(nil)
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", "runbooks/repair_web.yaml"); err != nil ||
		!strings.Contains(out, "no issues found") || strings.Contains(out, "[warning]") {
		t.Fatalf("validate: %v\n%s", err, out)
	}

	// 1. A check starts nothing and calls no model, and says it is
	// incomplete, since a command inside a container cannot be checked.
	rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/repair_web.yaml", "--mode", "check")
	if code != 3 || len(rep.Tasks) == 0 || rep.Tasks[0].Status != "would_change" {
		t.Fatalf("check: exit %d, want 3 with the container start named as a change\n%s", code, raw)
	}
	if host.Chats() != 0 || exec.Command("docker", "inspect", name).Run() == nil {
		t.Fatalf("the check made %d model call(s), or left a container", host.Chats())
	}

	// 2. The disk fills, the model names free_space, and only that fix runs.
	host.Answer = `{"fix":"free_space","confidence":0.9,"reason":"the filesystem under the document root is full"}`
	rep, raw, code = runPleiadesJSON(t, dir, "run", "runbooks/repair_web.yaml", "-e", "fault=disk_full")
	if code != 0 || rep.Outcome.Status != "rescued" || rep.RunID == "" || host.Chats() != 1 {
		t.Fatalf("run: exit %d, %d model call(s)\n%s", code, host.Chats(), raw)
	}
	status := map[string]string{}
	for _, task := range rep.Tasks {
		status[task.Name] = task.Status
	}
	for task, want := range map[string]string{
		"Fix, free the full filesystem":        "changed",
		"Fix, make the document root readable": "skipped",
		"Fix, serve on port 8081 instead":      "skipped",
		"Health check again":                   "changed",
		"Say the model found no fix":           "skipped",
	} {
		if status[task] != want {
			t.Errorf("%q is %q, want %q", task, status[task], want)
		}
	}
	if exec.Command("docker", "inspect", name).Run() != nil {
		t.Fatal("the repaired container is not there")
	}

	// 3. A rollback removes it through container.run's recorded undo, and
	// runs nothing for a task whose rollback: list is empty.
	out, err := runPleiadesWithEnv(t, dir, env, "rollback", rep.RunID, "--allow-unknown", "tasks[0].block[7]")
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	for _, want := range []string{"undo tasks[0].block[0] (Start the container)", "nothing to undo: tasks[0].rescue[1] on workstation"} {
		if !strings.Contains(out, want) {
			t.Errorf("the rollback does not say %q:\n%s", want, out)
		}
	}
	if exec.Command("docker", "inspect", name).Run() == nil {
		t.Error("the container is still there after the rollback")
	}
}
