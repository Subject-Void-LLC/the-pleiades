//go:build integration

// This file seeds the inventory the Grand Integration Test targets, and
// holds the HTTP helpers the test drives the controller with.
//
// Seeding goes through the same ent.OpenDatabase seam and the same
// versioned migrations the controller itself uses. It deliberately does
// not call client.Schema.Create: that is ent's automatic diff-and-apply,
// which no production binary uses, and reaching for it here is what made
// the previous version of this test validate a database configuration
// that existed nowhere.
package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// seededDevice is one inventory fixture row plus the identifier the
// database generated for it, which later assertions compare the wire
// against.
type seededDevice struct {
	// name is the display name.
	name string

	// group is the inventory group this device belongs to.
	group string

	// host is the management address, or empty for the device that
	// deliberately has none.
	host string

	// deviceID is the UUIDv7 the schema generated. This is the value that
	// must appear on the wire, and comparing against it is what catches a
	// regression that puts the display name in the identifier field.
	deviceID string
}

// The inventory fixture.
//
// rtr1 and rtr2 are the targets. rtr3 and rtr4 are eligible in every
// single way except group membership, which is what isolates group
// filtering as the only variable: if the group predicate were dropped,
// they would be dispatched to and the assertions would fail. rtr5 is in
// the targeted group but has no host, so it is skipped for that reason
// alone.
//
// The result is three distinct tallies, 2 dispatched, 1 skipped, 0
// failed, so a bug that reported the same number for all three cannot
// pass.
const (
	targetGroup   = "edge"
	untargetGroup = "core"
)

// seedFixture returns the devices to seed, in a fixed order.
func seedFixture() []*seededDevice {
	return []*seededDevice{
		{name: "rtr1", group: targetGroup, host: "10.0.0.1"},
		{name: "rtr2", group: targetGroup, host: "10.0.0.2"},
		{name: "rtr3", group: untargetGroup, host: "10.0.0.3"},
		{name: "rtr4", group: untargetGroup, host: "10.0.0.4"},
		{name: "rtr5", group: targetGroup, host: ""},
	}
}

// seedInventory writes the fixture into the real database and records
// each generated device identifier.
//
// The envelope encryption hook is installed on the seeding client, built
// from the same master key the controller subprocess is given. Seeding
// plaintext would also work, since a properties map with no encryption
// marker is passed straight through on read, but it would never exercise
// the cross-process key path. Seeding encrypted means the controller has
// to decrypt rows it did not write, which is a real claim, and it makes a
// key mismatch fail loudly instead of silently.
func seedInventory(tb testing.TB, dsn string) []*seededDevice {
	tb.Helper()
	ctx := context.Background()

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		tb.Fatalf("opening the database to seed: %v", err)
	}
	defer client.Close()

	envelope, err := crypto.NewEnvelopeService(masterKeyBytes(tb), "v1", nil, "")
	if err != nil {
		tb.Fatalf("building the envelope service: %v", err)
	}
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(envelope))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(envelope))

	devices := seedFixture()
	byGroup := map[string][]*ent.Device{}

	for _, d := range devices {
		// A non-empty properties map even for the hostless device, so the
		// encryption hook fires for every row and the missing host key is
		// genuinely the only difference between rtr5 and the others.
		properties := map[string]interface{}{"role": "spare"}
		if d.host != "" {
			properties = map[string]interface{}{"host": d.host}
		}

		row := client.Device.Create().
			SetName(d.name).
			SetType("cisco_router").
			SetProperties(properties).
			SaveX(ctx)

		d.deviceID = row.DeviceID
		byGroup[d.group] = append(byGroup[d.group], row)
	}

	for _, group := range []string{targetGroup, untargetGroup} {
		client.Group.Create().SetName(group).AddDevices(byGroup[group]...).SaveX(ctx)
	}

	return devices
}

// masterKeyBytes decodes the harness master encryption key into the raw
// bytes crypto.NewEnvelopeService wants.
func masterKeyBytes(tb testing.TB) []byte {
	tb.Helper()
	key, err := base64.StdEncoding.DecodeString(harnessMasterKey)
	if err != nil {
		tb.Fatalf("decoding the harness master key: %v", err)
	}
	return key
}

// do issues one HTTP request against the controller and returns its
// status and body.
//
// bearer may be empty, which is how the unauthenticated case is
// exercised: the request then carries no Authorization header at all,
// rather than an empty one, which is a different thing.
func (h *harness) do(tb testing.TB, method, path, bearer string) (int, []byte) {
	tb.Helper()

	req, err := http.NewRequest(method, h.baseURL+path, nil)
	if err != nil {
		tb.Fatalf("building the %s %s request: %v", method, path, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatalf("issuing %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		tb.Fatalf("reading the %s %s response body: %v", method, path, err)
	}
	return resp.StatusCode, body
}

// dispatch launches runbookID against group and returns the raw response.
//
// The query string is assembled with url.Values so a group or runbook
// name carrying a reserved character cannot change the shape of the
// request, which matters because the fuzz target drives this same helper
// with arbitrary values.
func (h *harness) dispatch(tb testing.TB, bearer, group, runbook string) (int, []byte) {
	tb.Helper()
	query := url.Values{"group": {group}, "runbook": {runbook}}
	return h.do(tb, http.MethodPost, "/api/v1/jobs/dispatch?"+query.Encode(), bearer)
}

// waitForHTTPStatus polls path until it answers with want, or fails.
//
// This is a bounded poll against a real endpoint, not a sleep: it returns
// the moment the condition holds, and it fails with the subprocess's own
// output when it does not, which is the difference between a debuggable
// CI failure and a mystery.
func (h *harness) waitForHTTPStatus(tb testing.TB, path string, want int, what string) {
	tb.Helper()

	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	var lastStatus int
	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := http.Get(h.baseURL + path)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		lastStatus = resp.StatusCode
		resp.Body.Close()
		if resp.StatusCode == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	tb.Fatalf("timed out waiting for %s (%s returned status %d, last error %v)\n%s",
		what, path, lastStatus, lastErr, h.controller.output())
}

// jobResponse is the subset of the job view this package asserts on.
type jobResponse struct {
	JobID      string `json:"job_id"`
	RunbookID  string `json:"runbook_id"`
	GroupName  string `json:"group_name"`
	State      string `json:"state"`
	Dispatched int    `json:"dispatched"`
	Skipped    int    `json:"skipped"`
	Failed     int    `json:"failed"`
	Tasks      []struct {
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		Outcome    string `json:"outcome"`
		Reason     string `json:"reason"`
	} `json:"tasks"`
}

// pollJobUntilTerminal polls the real job endpoint until the job reaches
// a terminal state.
//
// This is deliberately the production mechanism. A caller of this API is
// told to poll GET /jobs/{id}, so polling it here exercises the same
// path a real client uses, rather than reaching into the job store the
// way the previous version of this test did.
func (h *harness) pollJobUntilTerminal(tb testing.TB, bearer, jobID string) jobResponse {
	tb.Helper()

	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	var last jobResponse

	for time.Now().Before(deadline) {
		status, body := h.do(tb, http.MethodGet, "/api/v1/jobs/"+jobID, bearer)
		if status != http.StatusOK {
			tb.Fatalf("GET /api/v1/jobs/%s returned status %d, body %s", jobID, status, body)
		}
		if err := json.Unmarshal(body, &last); err != nil {
			tb.Fatalf("decoding the job view: %v (body %s)", err, body)
		}
		if last.State == "completed" || last.State == "failed" {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}

	tb.Fatalf("job %s did not reach a terminal state within its budget; last observed state %q\n%s",
		jobID, last.State, h.controller.output())
	return last
}

// requireStringField pulls one string field out of a JSON object body,
// failing the test if the body does not decode or the field is absent.
func requireStringField(tb testing.TB, body []byte, field string) string {
	tb.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		tb.Fatalf("decoding the response body: %v (body %s)", err, body)
	}
	value, ok := decoded[field].(string)
	if !ok {
		tb.Fatalf("the response carries no string field %q: %s", field, body)
	}
	return value
}

// describeTasks renders a job's task list for a failure message.
func describeTasks(job jobResponse) string {
	parts := make([]string, 0, len(job.Tasks))
	for _, task := range job.Tasks {
		parts = append(parts, fmt.Sprintf("%s=%s(%s)", task.DeviceName, task.Outcome, task.Reason))
	}
	return strings.Join(parts, " ")
}
