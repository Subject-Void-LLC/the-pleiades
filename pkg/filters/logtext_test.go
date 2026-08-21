package filters_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestSyslogParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "rfc5424_full",
			in:   `<34>1 2003-10-11T22:14:15Z host su - ID47 - login ok`,
			want: map[string]any{
				"format": "rfc5424", "facility": 4, "severity": 2, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "su", "proc_id": "-", "msg_id": "ID47",
				"structured_data": "-", "message": "login ok",
			},
		},
		{
			name: "rfc5424_with_structured_data",
			in:   `<165>1 2003-10-11T22:14:15Z host app 1234 ID1 [ex@1 a="1" b="two words"] the message`,
			want: map[string]any{
				"format": "rfc5424", "facility": 20, "severity": 5, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "1234", "msg_id": "ID1",
				"structured_data": `[ex@1 a="1" b="two words"]`, "message": "the message",
			},
		},
		{
			name: "rfc5424_multiple_structured_data_elements",
			in:   `<13>1 2003-10-11T22:14:15Z host app - - [a@1 x="1"][b@1 y="2"] msg`,
			want: map[string]any{
				"format": "rfc5424", "facility": 1, "severity": 5, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "-", "msg_id": "-",
				"structured_data": `[a@1 x="1"][b@1 y="2"]`, "message": "msg",
			},
		},
		{
			name: "rfc5424_structured_data_escaped_bracket_in_value",
			in:   `<13>1 2003-10-11T22:14:15Z host app - - [a@1 x="va\]lue"] msg`,
			want: map[string]any{
				"format": "rfc5424", "facility": 1, "severity": 5, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "-", "msg_id": "-",
				"structured_data": `[a@1 x="va\]lue"]`, "message": "msg",
			},
		},
		{
			name: "rfc5424_no_message",
			in:   `<13>1 2003-10-11T22:14:15Z host app - - -`,
			want: map[string]any{
				"format": "rfc5424", "facility": 1, "severity": 5, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "-", "msg_id": "-",
				"structured_data": "-", "message": "",
			},
		},
		{
			name: "rfc5424_short_missing_trailing_fields",
			in:   `<34>1 short`,
			want: map[string]any{
				"format": "rfc5424", "facility": 4, "severity": 2, "version": 1,
				"timestamp": "short", "hostname": "", "app_name": "", "proc_id": "",
				"msg_id": "", "structured_data": "", "message": "",
			},
		},
		{
			name: "rfc5424_no_content_after_msgid",
			in:   `<34>1 2003-10-11T22:14:15Z host app - ID1`,
			want: map[string]any{
				"format": "rfc5424", "facility": 4, "severity": 2, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "-", "msg_id": "ID1",
				"structured_data": "", "message": "",
			},
		},
		{
			name: "rfc5424_no_sd_marker_malformed",
			in:   `<34>1 2003-10-11T22:14:15Z host app - ID1 plain message`,
			want: map[string]any{
				"format": "rfc5424", "facility": 4, "severity": 2, "version": 1,
				"timestamp": "2003-10-11T22:14:15Z", "hostname": "host",
				"app_name": "app", "proc_id": "-", "msg_id": "ID1",
				"structured_data": "", "message": "plain message",
			},
		},
		{
			name: "rfc3164_no_colon_separator",
			in:   `<34>Oct 11 22:14:15 host justamessagewithnocolon`,
			want: map[string]any{
				"format": "rfc3164", "facility": 4, "severity": 2, "version": 0,
				"timestamp": "Oct 11 22:14:15", "hostname": "host",
				"app_name": "", "proc_id": "", "msg_id": "",
				"structured_data": "", "message": "justamessagewithnocolon",
			},
		},
		{
			name: "rfc3164_with_pid",
			in:   `<34>Oct 11 22:14:15 host su[123]: login ok`,
			want: map[string]any{
				"format": "rfc3164", "facility": 4, "severity": 2, "version": 0,
				"timestamp": "Oct 11 22:14:15", "hostname": "host",
				"app_name": "su", "proc_id": "123", "msg_id": "",
				"structured_data": "", "message": "login ok",
			},
		},
		{
			name: "rfc3164_without_pid",
			in:   `<38>Oct 1 06:00:01 host CRON: (root) CMD (test)`,
			want: map[string]any{
				"format": "rfc3164", "facility": 4, "severity": 6, "version": 0,
				"timestamp": "Oct 1 06:00:01", "hostname": "host",
				"app_name": "CRON", "proc_id": "", "msg_id": "",
				"structured_data": "", "message": "(root) CMD (test)",
			},
		},
		{
			name: "rfc3164_no_recognizable_header",
			in:   `<38>whatever this is`,
			want: map[string]any{
				"format": "rfc3164", "facility": 4, "severity": 6, "version": 0,
				"timestamp": "", "hostname": "", "app_name": "", "proc_id": "",
				"msg_id": "", "structured_data": "", "message": "whatever this is",
			},
		},
		{"missing_angle_brackets", "no pri here", nil},
		{"malformed_pri_not_numeric", "<abc>rest", nil},
		{"malformed_pri_out_of_range", "<192>rest", nil},
		{"malformed_pri_negative_looking", "<->rest", nil},
		{"malformed_pri_unterminated", "<34no closing bracket", nil},
		{"empty", "", nil},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.SyslogParse(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SyslogParse(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLineEndingConvert(t *testing.T) {
	cases := []struct{ name, content, style, want string }{
		{"mixed_to_lf", "a\r\nb\nc\rd", "lf", "a\nb\nc\nd"},
		{"lf_to_crlf", "a\nb\nc", "crlf", "a\r\nb\r\nc"},
		{"crlf_to_crlf_noop", "a\r\nb", "crlf", "a\r\nb"},
		{"style_case_insensitive", "a\nb", "CRLF", "a\r\nb"},
		{"no_line_endings", "abc", "lf", "abc"},
		{"unrecognized_style", "a\nb", "cr", ""},
		{"empty_style", "a\nb", "", ""},
		{"empty_content", "", "lf", ""},
		{"over_cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), "lf", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.LineEndingConvert(tc.content, tc.style); got != tc.want {
				t.Errorf("LineEndingConvert(%q, %q) = %q, want %q", tc.content, tc.style, got, tc.want)
			}
		})
	}
}

func TestTrimNormalizeWhitespace(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"internal_runs_collapsed", "a   b\tc", "a b c"},
		{"leading_trailing_trimmed", "  a b  ", "a b"},
		{"newlines_collapsed", "a\n\nb", "a b"},
		{"already_normalized", "a b c", "a b c"},
		{"only_whitespace", "   \t\n  ", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.TrimNormalizeWhitespace(tc.in); got != tc.want {
				t.Errorf("TrimNormalizeWhitespace(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPayloadChunker(t *testing.T) {
	cases := []struct {
		name  string
		items []any
		size  int
		want  []any
	}{
		{"exact_multiple", []any{1, 2, 3, 4}, 2, []any{[]any{1, 2}, []any{3, 4}}},
		{"remainder_chunk", []any{1, 2, 3, 4, 5}, 2, []any{[]any{1, 2}, []any{3, 4}, []any{5}}},
		{"size_larger_than_items", []any{1, 2}, 5, []any{[]any{1, 2}}},
		{"size_one", []any{1, 2, 3}, 1, []any{[]any{1}, []any{2}, []any{3}}},
		{"empty_items_valid_size", []any{}, 2, []any{}},
		{"zero_size", []any{1, 2}, 0, nil},
		{"negative_size", []any{1, 2}, -1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.PayloadChunker(tc.items, tc.size)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PayloadChunker(%v, %d) = %#v, want %#v", tc.items, tc.size, got, tc.want)
			}
		})
	}
}
