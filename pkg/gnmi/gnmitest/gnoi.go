// Package gnmitest: the gNOI services a server answers beside gNMI, on the
// same port and behind the same credential check: System.Time,
// File.Stat (the files Options.Files names) and
// CertificateManagement.GetCertificates (the server's own certificate).
package gnmitest

import (
	"context"
	"encoding/pem"
	"time"

	"github.com/openconfig/gnoi/cert"
	"github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/system"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gnoiSystem answers System.Time.
type gnoiSystem struct {
	system.UnimplementedSystemServer
}

// Time implements system.SystemServer: the server's own clock.
func (gnoiSystem) Time(context.Context, *system.TimeRequest) (*system.TimeResponse, error) {
	return &system.TimeResponse{Time: uint64(time.Now().UnixNano())}, nil // #nosec G115 -- a time after 1970
}

// gnoiFile answers File.Stat.
type gnoiFile struct {
	file.UnimplementedFileServer
	s *Server
}

// Stat implements file.FileServer: the file Options.Files names at path,
// its permissions written as gNOI writes them, the octal digits as a decimal
// number (644), as Nokia SR Linux answers.
func (f gnoiFile) Stat(_ context.Context, req *file.StatRequest) (*file.StatResponse, error) {
	size, ok := f.s.opts.Files[req.GetPath()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no file at %s", req.GetPath())
	}
	return &file.StatResponse{Stats: []*file.StatInfo{{Path: req.GetPath(), Size: size, Permissions: 644, LastModified: uint64(time.Now().UnixNano())}}}, nil // #nosec G115 -- a time after 1970
}

// gnoiCert answers CertificateManagement.GetCertificates.
type gnoiCert struct {
	cert.UnimplementedCertificateManagementServer
	s *Server
}

// GetCertificates implements cert.CertificateManagementServer: the
// server's own certificate, or none for a plaintext server.
func (c gnoiCert) GetCertificates(context.Context, *cert.GetCertificatesRequest) (*cert.GetCertificatesResponse, error) {
	resp := &cert.GetCertificatesResponse{}
	if leaf := c.s.leaf; leaf != nil {
		resp.CertificateInfo = append(resp.CertificateInfo, &cert.CertificateInfo{
			CertificateId: "gnmitest",
			Certificate: &cert.Certificate{
				Type:        cert.CertificateType_CT_X509,
				Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
			},
			ModificationTime: leaf.NotBefore.UnixNano(),
		})
	}
	return resp, nil
}
