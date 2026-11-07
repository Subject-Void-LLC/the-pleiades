// Package clireport_test: the version report.
package clireport_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

func TestNewVersionReportNamesTheProtocolWindow(t *testing.T) {
	r := clireport.NewVersionReport("runner", "0.4.0")
	if r.SchemaVersion != clireport.VersionReportVersion || r.Program != "runner" || r.Version != "0.4.0" {
		t.Errorf("report %+v", r)
	}
	if r.ExtensionProtocols.Current != external.ProtocolVersion || !reflect.DeepEqual(r.ExtensionProtocols.Versions, external.Protocols) {
		t.Errorf("protocols %+v, want external.Protocols", r.ExtensionProtocols)
	}
	if dev := clireport.NewVersionReport("pleiades", "0.0.0-dev+abc"); dev.Codename != "" {
		t.Errorf("a development build has codename %q", dev.Codename)
	}
	if _, err := clireport.VersionSchema.Build("../.."); err != nil {
		t.Errorf("the version schema does not build: %v", err)
	}
}
