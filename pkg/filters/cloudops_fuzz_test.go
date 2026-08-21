package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// This file fuzzes the string-parsing functions in cloudops.go:
// FormatCurrency (a decimal-amount parser), CloudInitWrap (a MIME
// envelope builder driven by arbitrary content/contentType text), and
// IAMPolicyMerger (a JSON parser), the same "every non-trivial parser
// gets a fuzz target" convention pki_fuzz_test.go's own comment states.
// AWSTagListToMap/MapToAWSTagList/ExtractPaginationToken take a map, not
// a string, so testing.F's corpus types cannot drive them directly, the
// same reason structured_fuzz_test.go never fuzzes DeepMerge/Flatten's
// own map-shaped arguments either.

func FuzzFormatCurrency(f *testing.F) {
	seeds := []struct{ amount, code string }{
		{"1234.5", "USD"}, {"-42.1", "JPY"}, {"0", "KWD"}, {"", "USD"},
		{"1e10", "USD"}, {"12.", "EUR"}, {"not-a-number", "XYZ"},
	}
	for _, s := range seeds {
		f.Add(s.amount, s.code)
	}
	f.Fuzz(func(t *testing.T, amount, code string) {
		filters.FormatCurrency(amount, code)
	})
}

func FuzzCloudInitWrap(f *testing.F) {
	seeds := []struct{ content, contentType string }{
		{"#!/bin/bash\necho hi\n", ""},
		{"packages:\n  - nginx\n", "text/cloud-config"},
		{"", ""},
		{"embedded\nnewlines\nand\ttabs", "text/plain; charset=weird\ncontent"},
	}
	for _, s := range seeds {
		f.Add(s.content, s.contentType)
	}
	f.Fuzz(func(t *testing.T, content, contentType string) {
		filters.CloudInitWrap(content, contentType)
	})
}

func FuzzIAMPolicyMerger(f *testing.F) {
	seeds := []struct{ a, b string }{
		{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`, `{"Statement":[]}`},
		{`{"Statement":{"Effect":"Allow"}}`, `not json`},
		{"", ""},
		{`{"Statement":"oops"}`, `{"Statement":[1,2,3]}`},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		filters.IAMPolicyMerger(a, b)
	})
}
