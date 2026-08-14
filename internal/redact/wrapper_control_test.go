package redact_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// This file is a negative control.
//
// handler_test.go asserts that masking through
// slog.HandlerOptions.ReplaceAttr catches attributes added with Logger.With
// and catches the message body. Those tests pass. On their own that proves
// the chosen design works; it does not prove the rejected design would have
// failed, and a constraint recorded in a specification before either
// mechanism existed deserves better than "the thing we built works."
//
// LESSONS_LEARNED.md #95 states the rule: prove an assertion can fail
// before believing it passes, and a negative control that does not fail may
// have found real defense in depth, so keep opening layers until it does.
//
// So this file builds the rejected design, a wrapping slog.Handler that
// masks inside Handle, and demonstrates that it leaks. It is a permanent
// test rather than a throwaway experiment for two reasons. It is the only
// artifact in the repository that shows why the ordering constraint is a
// constraint rather than a preference. And if a future Go release changes
// how commonHandler.withAttrs pre-formats attributes, this test starts
// failing, which is the notification anyone re-examining the decision would
// want.

// wrappingMasker is the design this package deliberately does not use: a
// slog.Handler that decorates another and masks inside Handle.
//
// It is written the way somebody would write it in good faith, masking
// every attribute it can reach and the message too. The point is that good
// faith is not the problem.
type wrappingMasker struct {
	inner  slog.Handler
	masker *redact.Masker
}

// Enabled delegates.
func (h wrappingMasker) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle masks what it can see, then delegates.
func (h wrappingMasker) Handle(ctx context.Context, r slog.Record) error {
	masked := slog.NewRecord(r.Time, r.Level, h.masker.Text(nil, r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		masked.AddAttrs(h.masker.Attr(nil, a))
		return true
	})
	return h.inner.Handle(ctx, masked)
}

// WithAttrs delegates, which is the whole problem: the attributes are
// pre-formatted by the inner handler at this moment, and Handle never sees
// them again as attributes.
func (h wrappingMasker) WithAttrs(attrs []slog.Attr) slog.Handler {
	return wrappingMasker{inner: h.inner.WithAttrs(attrs), masker: h.masker}
}

// WithGroup delegates.
func (h wrappingMasker) WithGroup(name string) slog.Handler {
	return wrappingMasker{inner: h.inner.WithGroup(name), masker: h.masker}
}

// TestAWrappingHandlerLeaksAttributesAddedWithWith is the control for
// TestReplaceAttrMasksLoggerWithAttrs.
//
// The two tests log the identical line. This one asserts the secret is
// still there.
func TestAWrappingHandlerLeaksAttributesAddedWithWith(t *testing.T) {
	t.Parallel()

	const secret = "wrapper-control-secret-value"

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	var buf bytes.Buffer
	// No ReplaceAttr: masking is the wrapper's job in this design.
	logger := slog.New(wrappingMasker{inner: slog.NewJSONHandler(&buf, nil), masker: m})

	logger.With("password", secret).Info("connecting")

	if !strings.Contains(buf.String(), secret) {
		t.Fatalf(
			"the wrapping handler masked an attribute added with Logger.With, which it should be structurally unable to do.\n"+
				"Either the standard library changed how commonHandler.withAttrs pre-formats attributes, or this control no "+
				"longer exercises the case it was written for. Re-derive the ordering constraint before trusting it.\n"+
				"Output was:\n%s", buf.String())
	}

	// The same line through ReplaceAttr, for contrast, in the same test, so
	// the comparison is visible rather than spread across two files.
	var masked bytes.Buffer
	correct := slog.New(slog.NewJSONHandler(&masked, m.HandlerOptions(slog.LevelDebug)))
	correct.With("password", secret).Info("connecting")

	if strings.Contains(masked.String(), secret) {
		t.Fatalf("ReplaceAttr also leaked, so the constraint this package is built on does not hold:\n%s", masked.String())
	}
}

// TestAWrappingHandlerDoesReachADirectAttribute is the other half of the
// control, and it is why the wrapper is a trap rather than an obvious
// mistake.
//
// A wrapper handles the common case correctly. Somebody testing it with a
// direct attribute would see it work, ship it, and leak only on the call
// sites that use Logger.With, which is the idiomatic way to attach context
// and therefore the way the interesting secrets travel.
func TestAWrappingHandlerDoesReachADirectAttribute(t *testing.T) {
	t.Parallel()

	const secret = "wrapper-direct-secret-value"

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(wrappingMasker{inner: slog.NewJSONHandler(&buf, nil), masker: m})

	logger.Info("connecting", "password", secret)

	if strings.Contains(buf.String(), secret) {
		t.Fatalf("the wrapping handler failed even the case it does handle, so this control proves nothing:\n%s", buf.String())
	}
}
