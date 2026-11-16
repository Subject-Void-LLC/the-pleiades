// Package devicetls: which alerts, on which connections, read as this
// side's client certificate refused, and which failures are worth a
// diagnosis.
package devicetls

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

// remote is the error Go's client returns for an alert the peer sent.
func remote(alert tls.AlertError) error {
	return fmt.Errorf("read: %w", &net.OpError{Op: remoteAlert, Err: errors.New(alert.Error())})
}

// TestConnRecord_OnlyAConnectionThatAskedRefusesACertificate holds every
// alert to its connection's own record: a server or a middlebox that never
// asked for a certificate is refusing something a certificate cannot change,
// and certificate_required alone names its own cause.
func TestConnRecord_OnlyAConnectionThatAskedRefusesACertificate(t *testing.T) {
	for _, alert := range clientCertificateAlerts {
		if _, ok := (connRecord{}).refusal(remote(alert), nil); ok != (alert == alertCertificateRequired) {
			t.Errorf("alert %s on a connection that never asked: refusal %v", alert, ok)
		}
		refused, ok := (connRecord{asked: true, presented: true}).refusal(remote(alert), io.EOF)
		if !ok || !refused.Presented || !errors.Is(refused, io.EOF) {
			t.Errorf("alert %s on a connection that asked = %+v, %v", alert, refused, ok)
		}
	}
	for _, err := range []error{remote(120), errors.New("tls: bad certificate"), fmt.Errorf("x%w", tls.AlertError(42))} {
		if _, ok := (connRecord{asked: true}).refusal(err, nil); ok {
			t.Errorf("%v read as a refused client certificate", err)
		}
	}
}

// TestWorthDiagnosing_IsAnythingButTime holds the diagnosis to failures a
// refused certificate can take the shape of, which is any but a timeout or
// the caller's cancellation, after which there is no time to look.
func TestWorthDiagnosing_IsAnythingButTime(t *testing.T) {
	ctx := context.Background()
	for _, err := range []error{remote(42), &net.OpError{Op: "read", Err: syscall.ECONNRESET}, io.EOF, errors.New("http2: client conn could not be established")} {
		if !worthDiagnosing(ctx, err) {
			t.Errorf("worthDiagnosing(%v) = false", err)
		}
	}
	for _, err := range []error{&net.OpError{Op: "read", Err: context.DeadlineExceeded}, fmt.Errorf("x: %w", context.Canceled)} {
		if worthDiagnosing(ctx, err) {
			t.Errorf("worthDiagnosing(%v) = true", err)
		}
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if worthDiagnosing(done, io.EOF) {
		t.Error("a failure after the caller gave up was diagnosed")
	}
}

// TestWatch_ANilConfigurationDiagnosesNothing holds a Watch over no TLS (an
// http:// device) to explaining a failure as it is and dialing nothing.
func TestWatch_ANilConfigurationDiagnosesNothing(t *testing.T) {
	cfg, w := Watched(nil, "127.0.0.1:1")
	if cfg != nil || w.Asked() {
		t.Fatal("a nil configuration was watched")
	}
	w.asked = true
	if _, ok := w.Diagnose(context.Background(), nil); ok {
		t.Error("a Watch with no configuration diagnosed a refusal")
	}
	if got := w.Explain(context.Background(), io.EOF); got != io.EOF {
		t.Errorf("Explain(EOF) = %v", got)
	}
}
