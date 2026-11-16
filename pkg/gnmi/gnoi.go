// Package gnmi: a small, read-only subset of gNOI, the operational services
// a gNMI server often serves beside it on the same connection: the device's
// clock (System.Time), a file's metadata (File.Stat) and its installed
// certificates (CertificateManagement.GetCertificates). Nothing here changes
// a device: reboot, file transfer and certificate rotation are not built.
package gnmi

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/openconfig/gnoi/cert"
	"github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/system"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// The bounds on a gNOI answer.
const (
	// MaxStatEntries bounds the files one Stat may report.
	MaxStatEntries = 10000
	// MaxCertificates bounds the certificates one answer may report.
	MaxCertificates = 1000
	// maxFilePathBytes bounds a file path asked about or answered.
	maxFilePathBytes = 4096
)

// Time asks the device for its clock (gNOI System.Time). A time that does not
// fit a signed 64-bit count of nanoseconds is refused.
func (c *Client) Time(ctx context.Context) (time.Time, error) {
	if err := c.refused(); err != nil {
		return time.Time{}, err
	}
	call, cancel := c.call(ctx)
	defer cancel()
	resp, err := system.NewSystemClient(c.conn).Time(call, &system.TimeRequest{})
	if err != nil {
		return time.Time{}, c.fail(call, "System.Time", err)
	}
	if resp.GetTime() > math.MaxInt64 {
		return time.Time{}, fmt.Errorf("gnmi System.Time: the device sent %d, past any time this client can read", resp.GetTime())
	}
	return time.Unix(0, int64(resp.GetTime())).UTC(), nil // #nosec G115 -- bounded above
}

// FileInfo is one file a device's File.Stat reported.
type FileInfo struct {
	// Path is the file's path on the device.
	Path string
	// LastModified is when it was last written.
	LastModified time.Time
	// Permissions are its POSIX permission bits as gNOI writes them: the
	// octal digits read as a decimal number, 644 for rw-r--r-- (Nokia SR
	// Linux 26.7.2, measured 2026-11-15).
	Permissions uint32
	// Size is its size in bytes.
	Size uint64
	// Umask is the umask the device reports for it.
	Umask uint32
}

// FileStat asks the device about path (gNOI File.Stat): one file, or each
// file in a directory. A path that is empty, too long or not printable is
// refused before it is sent, and an answered path that is not printable is
// refused with the answer.
func (c *Client) FileStat(ctx context.Context, path string) ([]FileInfo, error) {
	if err := c.refused(); err != nil {
		return nil, err
	}
	if path == "" || len(path) > maxFilePathBytes || !yanglib.Printable(path) {
		return nil, fmt.Errorf("gnmi File.Stat: the path %q is empty, longer than %d bytes, or not printable", path, maxFilePathBytes)
	}
	call, cancel := c.call(ctx)
	defer cancel()
	resp, err := file.NewFileClient(c.conn).Stat(call, &file.StatRequest{Path: path})
	if err != nil {
		return nil, c.fail(call, "File.Stat", err)
	}
	if len(resp.GetStats()) > MaxStatEntries {
		return nil, fmt.Errorf("gnmi File.Stat: the device reported %d files, more than %d", len(resp.GetStats()), MaxStatEntries)
	}
	out := make([]FileInfo, 0, len(resp.GetStats()))
	for _, s := range resp.GetStats() {
		if len(s.GetPath()) > maxFilePathBytes || !yanglib.Printable(s.GetPath()) {
			return nil, fmt.Errorf("gnmi File.Stat: the device reported a path that is too long or not printable: %q", truncate(s.GetPath(), 120))
		}
		if s.GetLastModified() > math.MaxInt64 {
			return nil, fmt.Errorf("gnmi File.Stat: the device reported a time past any this client can read for %s", s.GetPath())
		}
		out = append(out, FileInfo{
			Path:         s.GetPath(),
			LastModified: time.Unix(0, int64(s.GetLastModified())).UTC(), // #nosec G115 -- bounded above
			Permissions:  s.GetPermissions(),
			Size:         s.GetSize(),
			Umask:        s.GetUmask(),
		})
	}
	return out, nil
}

// CertificateInfo is one certificate a device reports installed.
type CertificateInfo struct {
	// ID is the device's name for it.
	ID string
	// Type is the gNOI certificate type, such as CT_X509.
	Type string
	// Subject and Issuer are its names, as text; empty when it is not a
	// PEM X.509 certificate this client could read.
	Subject, Issuer string
	// NotAfter is the last day it is valid, YYYY-MM-DD in UTC.
	NotAfter string
	// Fingerprint is its SHA-256 in the form tls_fingerprint pins.
	Fingerprint string
	// Modified is when the device last changed it.
	Modified time.Time
}

// Certificates asks the device which certificates it has installed (gNOI
// CertificateManagement.GetCertificates). Each is parsed, never trusted: what
// a device reports is what it says, and only a handshake proves a key.
func (c *Client) Certificates(ctx context.Context) ([]CertificateInfo, error) {
	if err := c.refused(); err != nil {
		return nil, err
	}
	call, cancel := c.call(ctx)
	defer cancel()
	resp, err := cert.NewCertificateManagementClient(c.conn).GetCertificates(call, &cert.GetCertificatesRequest{})
	if err != nil {
		return nil, c.fail(call, "CertificateManagement.GetCertificates", err)
	}
	if len(resp.GetCertificateInfo()) > MaxCertificates {
		return nil, fmt.Errorf("gnmi CertificateManagement.GetCertificates: the device reported %d certificates, more than %d", len(resp.GetCertificateInfo()), MaxCertificates)
	}
	out := make([]CertificateInfo, 0, len(resp.GetCertificateInfo()))
	for _, info := range resp.GetCertificateInfo() {
		id := info.GetCertificateId()
		if !keepable(id) {
			return nil, fmt.Errorf("gnmi CertificateManagement.GetCertificates: the device reported a certificate id that is too long or not printable: %q", truncate(id, 120))
		}
		entry := CertificateInfo{ID: id, Type: info.GetCertificate().GetType().String(), Modified: time.Unix(0, info.GetModificationTime()).UTC()}
		if block, _ := pem.Decode(info.GetCertificate().GetCertificate()); block != nil && block.Type == "CERTIFICATE" {
			if parsed, err := x509.ParseCertificate(block.Bytes); err == nil {
				// A name holding a control character is the device's to
				// explain, and is left out rather than printed.
				if subject := parsed.Subject.String(); keepable(subject) {
					entry.Subject = subject
				}
				if issuer := parsed.Issuer.String(); keepable(issuer) {
					entry.Issuer = issuer
				}
				entry.NotAfter = parsed.NotAfter.UTC().Format(time.DateOnly)
				entry.Fingerprint = devicetls.Fingerprint(parsed)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// truncate is s cut to at most n bytes on a character boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// maxQuotedBytes bounds the text an error quotes from a name, a key or a
// path a device (or a caller) supplied, as pkg/farside bounds a far side's
// words: a device may send a name or a key of megabytes, and an error quoting
// it whole would carry all of it into every log and report that keeps the
// error.
const maxQuotedBytes = farside.MaxBytes

// quoted is s for an error: cut on a character boundary to maxQuotedBytes,
// marked with "..." when cut, and quoted, so a control character in it is
// shown escaped rather than acted on.
func quoted(s string) string {
	if len(s) <= maxQuotedBytes {
		return strconv.Quote(s)
	}
	return strconv.Quote(truncate(s, maxQuotedBytes)) + "..."
}

// shown is a path for an error, cut as quoted cuts text but not quoted: a
// path that passed Validate holds no control or format character, so only
// its length needs a bound.
func shown(p Path) string {
	s := p.String()
	if len(s) <= maxQuotedBytes {
		return s
	}
	return truncate(s, maxQuotedBytes) + "..."
}
