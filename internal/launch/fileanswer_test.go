// This file pins the file-answer rule: what classifies as what, which of
// the two gates governs which class, and the bypasses the classifier is
// supposed to close.
//
// The bypass cases are the point. A content rule written as a denylist of
// magic numbers is defeated by a byte-order mark, a UTF-16 export or a zip
// container, and every one of those is a case below. The cases that are
// NOT closed are here too, asserted as accepted, because a limitation
// nothing tests is a limitation somebody later assumes was handled.
package launch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

func TestClassifyFileContent(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    launch.FileContentClass
	}{
		{"plain text", []byte("host-a\nhost-b\n"), launch.FileInertText},
		{"empty", nil, launch.FileInertText},
		{"a PEM", []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), launch.FileInertText},
		{"JSON", []byte(`{"region":"eu-west-1"}`), launch.FileInertText},
		{"multi-byte text", []byte("naïve café 日本語 🗄 ok\n"), launch.FileInertText},

		{"a shebang", []byte("#!/bin/sh\necho hi\n"), launch.FileProgramText},
		{"a shebang with no space", []byte("#!/usr/bin/env python3\n"), launch.FileProgramText},

		// The offset-zero rule, both directions. A kernel honours the
		// interpreter line only at byte zero, so a file with a leading
		// blank line is not run as a program by that mechanism and is not
		// classified as one.
		{"a shebang after a newline is not one", []byte("\n#!/bin/sh\n"), launch.FileInertText},
		{"a shebang after a space is not one", []byte(" #!/bin/sh\n"), launch.FileInertText},

		// Each of these is a documented way past a magic-number denylist.
		{"a UTF-8 BOM then a shebang", append([]byte{0xEF, 0xBB, 0xBF}, "#!/bin/sh\n"...), launch.FileBinary},
		{"a UTF-16LE BOM", []byte{0xFF, 0xFE, 0x23, 0x00, 0x21, 0x00}, launch.FileBinary},
		{"a UTF-16BE BOM", []byte{0xFE, 0xFF, 0x00, 0x23, 0x00, 0x21}, launch.FileBinary},
		{"BOM-less UTF-16LE", []byte{0x68, 0x00, 0x6F, 0x00, 0x73, 0x00, 0x74, 0x00}, launch.FileBinary},
		{"an ELF header", []byte{0x7F, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, launch.FileBinary},
		{"a DOS/PE header", append([]byte("MZ"), 0x90, 0x00, 0x03, 0x00), launch.FileBinary},
		{"a zip container", []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00}, launch.FileBinary},
		{"gzip", []byte{0x1F, 0x8B, 0x08, 0x00}, launch.FileBinary},
		{"invalid UTF-8", []byte{0xC3, 0x28}, launch.FileBinary},
		{"a lone NUL in otherwise good text", []byte("host-a\x00host-b"), launch.FileBinary},

		// NOT closed, and asserted so nobody assumes otherwise. See the
		// residual-risk comment in fileanswer.go.
		{"base64 of an ELF is text and is accepted", []byte("f0VMRgIBAQA="), launch.FileInertText},
		{"a shell script with no interpreter line is text", []byte("rm -rf /\n"), launch.FileInertText},
		{"a PowerShell script is text", []byte("Get-ChildItem | Remove-Item\n"), launch.FileInertText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := launch.ClassifyFileContent(tc.content); got != tc.want {
				t.Errorf("ClassifyFileContent(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

// TestFileContentClassString covers the names a class renders under, which
// nothing in the product calls.
//
// It is reached only through %v in the table test above, and only when that
// test FAILS, so a passing run never executes it. That is exactly why it
// needs a test of its own: the method exists to make the most important
// failure message in this file legible, and a bare int in that message
// would say "= 0, want 2" about a rule nobody could then diagnose.
func TestFileContentClassString(t *testing.T) {
	cases := map[launch.FileContentClass]string{
		launch.FileBinary:      "binary",
		launch.FileProgramText: "program text",
		launch.FileInertText:   "text",
	}
	for class, want := range cases {
		if got := class.String(); got != want {
			t.Errorf("FileContentClass(%d).String() = %q, want %q", class, got, want)
		}
	}
	// An unknown class reads as the refusing one, matching the zero value.
	if got := launch.FileContentClass(99).String(); got != "binary" {
		t.Errorf("an unrecognised class names itself %q, want %q", got, "binary")
	}
}

// TestFileAnswer_ANonStringAnswerIsRefused covers the type assertion a JSON
// caller can reach: the API decodes answers into map[string]any, so a file
// question answered with a number or an object arrives as neither a string
// nor anything coercible.
func TestFileAnswer_ANonStringAnswerIsRefused(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "cert", Label: "Cert", Type: launch.QuestionFile},
	}}
	for _, answer := range []any{42, true, map[string]any{"a": 1}} {
		_, err := survey.Resolve(map[string]any{"cert": answer}, launch.FilePolicy{})
		if err == nil {
			t.Errorf("Resolve accepted %T as a file answer", answer)
			continue
		}
		if !errors.Is(err, launch.ErrSurveyAnswer) {
			t.Errorf("Resolve(%T) error = %v, want ErrSurveyAnswer", answer, err)
		}
	}
}

// TestFileContentClassZeroValueRefuses is the fail-closed invariant, and it
// is worth its own test because it is a property of a constant declaration
// that no other test would notice being changed.
//
// A classification that never ran, a struct nobody filled in and a switch
// arm nobody wrote all produce the zero value. If the zero value were the
// accepting one, every one of those would accept.
func TestFileContentClassZeroValueRefuses(t *testing.T) {
	var zero launch.FileContentClass
	if zero != launch.FileBinary {
		t.Fatalf("the zero FileContentClass is %v, want FileBinary: an unset classification must refuse", zero)
	}
	var pol launch.FilePolicy
	if pol.AllowProgramContent {
		t.Fatal("the zero FilePolicy permits program content, so a Config nobody stamped would be armed")
	}
}

// TestFileAnswer_TheTwoGatesAreBothRequired is the security property: each
// gate alone admits nothing, and only the pair admits program text.
func TestFileAnswer_TheTwoGatesAreBothRequired(t *testing.T) {
	const script = "#!/bin/sh\necho hello\n"

	cases := []struct {
		name     string
		system   bool
		question bool
		wantErr  bool
	}{
		{"neither gate", false, false, true},
		{"only the deployment consents", true, false, true},
		{"only the question is marked", false, true, true},
		{"both gates open", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			survey := launch.Survey{Enabled: true, Questions: []launch.Question{{
				Variable: "script", Label: "Script", Type: launch.QuestionFile,
				AllowProgramContent: tc.question,
			}}}

			got, err := survey.Resolve(
				map[string]any{"script": script},
				launch.FilePolicy{AllowProgramContent: tc.system},
			)

			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Resolve accepted program text with system=%v question=%v, want a refusal",
					tc.system, tc.question)
			case tc.wantErr:
				if !errors.Is(err, launch.ErrProgramContent) {
					t.Fatalf("Resolve error = %v, want ErrProgramContent", err)
				}
				// The refusal has to name both gates, or the operator
				// cannot tell which of two people has to change something.
				if !strings.Contains(err.Error(), "PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT") {
					t.Errorf("the refusal does not name the deployment variable: %v", err)
				}
			case err != nil:
				t.Fatalf("Resolve refused program text with both gates open: %v", err)
			default:
				if got["script"] != script {
					t.Errorf("the accepted answer is %q, want the content unchanged", got["script"])
				}
			}
		})
	}
}

// TestFileAnswer_BinaryIsRefusedEvenWithBothGatesOpen proves the gates
// govern one class and not the type.
//
// Opening the deployment's consent must not widen what may be UPLOADED,
// only who decided. A template author who marks a question as accepting
// program content has not thereby asked to receive an ELF.
func TestFileAnswer_BinaryIsRefusedEvenWithBothGatesOpen(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{{
		Variable: "payload", Label: "Payload", Type: launch.QuestionFile,
		AllowProgramContent: true,
	}}}

	_, err := survey.Resolve(
		map[string]any{"payload": "\x7fELF\x02\x01\x01\x00"},
		launch.FilePolicy{AllowProgramContent: true},
	)
	if err == nil {
		t.Fatal("Resolve accepted a binary answer with both gates open")
	}
	if errors.Is(err, launch.ErrProgramContent) {
		t.Fatalf("a binary answer was refused as program content, which implies a gate could admit it: %v", err)
	}
	if !errors.Is(err, launch.ErrSurveyAnswer) {
		t.Fatalf("Resolve error = %v, want ErrSurveyAnswer", err)
	}
}

// TestFileAnswer_SizeBoundIsEnforcedAtTheByte covers both sides of the
// limit, because an off-by-one in a bound is invisible from either side
// alone.
func TestFileAnswer_SizeBoundIsEnforcedAtTheByte(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "hosts", Label: "Hosts", Type: launch.QuestionFile},
	}}

	atLimit := strings.Repeat("a", launch.MaxFileAnswerBytes)
	if _, err := survey.Resolve(map[string]any{"hosts": atLimit}, launch.FilePolicy{}); err != nil {
		t.Errorf("an answer of exactly %d bytes was refused: %v", launch.MaxFileAnswerBytes, err)
	}

	overLimit := atLimit + "a"
	_, err := survey.Resolve(map[string]any{"hosts": overLimit}, launch.FilePolicy{})
	if err == nil {
		t.Fatalf("an answer of %d bytes was accepted, one over the limit", len(overLimit))
	}
	if !strings.Contains(err.Error(), "at most") {
		t.Errorf("the size refusal does not say what the limit is: %v", err)
	}
}

// TestFileAnswer_ContentIsCarriedThroughUntouched guards the one thing an
// automation actually depends on.
//
// Whitespace at either end of a file is part of the file: a trailing
// newline is what makes a PEM parse. Every other text answer is trimmed on
// the way in, so this being different is worth a test rather than a
// comment.
func TestFileAnswer_ContentIsCarriedThroughUntouched(t *testing.T) {
	const pem = "\n-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n\n"

	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "cert", Label: "Certificate", Type: launch.QuestionFile},
	}}
	got, err := survey.Resolve(map[string]any{"cert": pem}, launch.FilePolicy{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got["cert"] != pem {
		t.Errorf("the answer was modified in transit:\n got %q\nwant %q", got["cert"], pem)
	}
}

// TestSurveyValidate_FileQuestionRules pins the save-time refusals, the
// ones a template author meets rather than an operator.
func TestSurveyValidate_FileQuestionRules(t *testing.T) {
	cases := []struct {
		name     string
		question launch.Question
		want     string
	}{
		{
			name: "a file question carrying a default",
			question: launch.Question{
				Variable: "cert", Label: "Cert", Type: launch.QuestionFile, Default: "-----BEGIN",
			},
			want: "must not carry a default",
		},
		{
			name: "a maximum above what the platform honours",
			question: launch.Question{
				Variable: "cert", Label: "Cert", Type: launch.QuestionFile,
				Max: launch.MaxFileAnswerBytes + 1,
			},
			want: "above the",
		},
		{
			// A flag that is stored, rendered and consulted by nothing is
			// a control that looks like a decision and is not one.
			name: "program content marked on a question that cannot carry it",
			question: launch.Question{
				Variable: "name", Label: "Name", Type: launch.QuestionText,
				AllowProgramContent: true,
			},
			want: "cannot accept program content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := launch.Survey{Enabled: true, Questions: []launch.Question{tc.question}}.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			if !errors.Is(err, launch.ErrInvalidSurvey) {
				t.Fatalf("Validate error = %v, want ErrInvalidSurvey", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestFileAnswer_ASecretTypeIsNotReplayed proves the type joins the
// existing replay refusal rather than needing a second rule beside it.
func TestFileAnswer_ASecretTypeIsNotReplayed(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "hosts", Label: "Hosts", Type: launch.QuestionText},
		{Variable: "kubeconfig", Label: "Kubeconfig", Type: launch.QuestionFile},
	}}

	secret := survey.SecretVariables()
	if len(secret) != 1 || secret[0] != "kubeconfig" {
		t.Fatalf("SecretVariables() = %v, want just the file question: a file answer is what a relaunch must not replay", secret)
	}
}

// TestEveryQuestionTypeIsAnswerable is the harness that makes the NEXT
// question type loud instead of silent.
//
// The compiler gives zero coverage on this axis. Question.check has a
// default arm, Validate's per-type switch has none, and questionField in
// the UI falls through to a plain text box, so a type added to the list and
// nowhere else compiles, saves, renders and fails only when somebody
// launches. This ranges over the declared list and refuses to let a type
// sit in it unacknowledged.
func TestEveryQuestionTypeIsAnswerable(t *testing.T) {
	// One minimal valid question per type, and a sample answer for it.
	// A type added to QuestionTypes without an entry here fails below,
	// which is the whole point.
	samples := map[launch.QuestionType]struct {
		question launch.Question
		answer   any
	}{
		launch.QuestionText:     {launch.Question{Type: launch.QuestionText}, "value"},
		launch.QuestionTextarea: {launch.Question{Type: launch.QuestionTextarea}, "several\nlines"},
		launch.QuestionPassword: {launch.Question{Type: launch.QuestionPassword}, "hunter2"},
		launch.QuestionInteger:  {launch.Question{Type: launch.QuestionInteger}, 4},
		launch.QuestionFloat:    {launch.Question{Type: launch.QuestionFloat}, 1.5},
		launch.QuestionChoice: {
			launch.Question{Type: launch.QuestionChoice, Choices: []string{"a", "b"}}, "a",
		},
		launch.QuestionMultiSelect: {
			launch.Question{Type: launch.QuestionMultiSelect, Choices: []string{"a", "b"}}, []string{"a"},
		},
		launch.QuestionFile: {launch.Question{Type: launch.QuestionFile}, "host-a\nhost-b\n"},
	}

	for _, kind := range launch.QuestionTypes() {
		t.Run(string(kind), func(t *testing.T) {
			sample, known := samples[kind]
			if !known {
				t.Fatalf("question type %q is offered by QuestionTypes() but this test does not know how to "+
					"answer it. Add a sample here, and check the other places a type is decided rather than "+
					"defaulted: Question.check, Survey.Validate, QuestionType.Secret, and questionField in "+
					"internal/ui/resources/templates.", kind)
			}

			q := sample.question
			q.Variable = "v"
			q.Label = "V"
			survey := launch.Survey{Enabled: true, Questions: []launch.Question{q}}

			if err := survey.Validate(); err != nil {
				t.Fatalf("a minimal %q question does not validate: %v", kind, err)
			}

			got, err := survey.Resolve(map[string]any{"v": sample.answer}, launch.FilePolicy{})
			if err != nil {
				t.Fatalf("a %q answer does not resolve: %v", kind, err)
			}
			if _, ok := got["v"]; !ok {
				t.Errorf("a %q answer resolved to nothing, so the run would see the variable unset", kind)
			}
		})
	}
}
