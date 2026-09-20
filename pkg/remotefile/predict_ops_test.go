//go:build !windows

// Package remotefile_test: tests that the predictions for writing, touching
// and linking a path match what the real operations leave.
package remotefile_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestPredictions_MatchWhatWriteTouchAndSymlinkLeave extends predict.go's
// promise past Apply: each case runs the real operation, and the attribute
// pass its callers send after it, through a real shell, then compares what
// the device holds with what the prediction said, key by key. A key the
// prediction leaves out is named, so a prediction cannot pass by knowing
// nothing, and each named key is one only the device decides.
//
// A write over an existing file passes the attributes the file already had,
// as file.copy and file.line do. Write replaces the file with a new one
// from mktemp, which starts at 0600, so the attribute pass is what carries
// the old mode over, and PredictWrite predicts that pass rather than the
// bare write.
func TestPredictions_MatchWhatWriteTouchAndSymlinkLeave(t *testing.T) {
	content := []byte("hello\n")
	const existingMode = 0o644

	tests := []struct {
		name     string
		existing bool                                               // start with a regular file carrying existingMode
		want     func(before remotefile.Info) remotefile.Attributes // what the caller asks Apply for
		predict  func(want remotefile.Attributes, before remotefile.Info, target string) remotefile.Prediction
		op       func(ctx context.Context, conn *remoteexec.Conn, path, target string) error
		unknown  []string
	}{
		{
			name: "a write over a file, keeping its attributes", existing: true,
			want: func(before remotefile.Info) remotefile.Attributes {
				return remotefile.Attributes{Mode: before.Mode, Owner: before.Owner, Group: before.Group}
			},
			predict: func(want remotefile.Attributes, before remotefile.Info, _ string) remotefile.Prediction {
				return remotefile.PredictWrite(want, before, int64(len(content)))
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, _ string) error {
				return remotefile.Write(ctx, conn, path, content)
			},
			unknown: []string{"mtime"},
		},
		{
			name: "a write over a file, changing its mode", existing: true,
			want: func(remotefile.Info) remotefile.Attributes { return remotefile.Attributes{Mode: "0600"} },
			predict: func(want remotefile.Attributes, before remotefile.Info, _ string) remotefile.Prediction {
				return remotefile.PredictWrite(want, before, int64(len(content)))
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, _ string) error {
				return remotefile.Write(ctx, conn, path, content)
			},
			unknown: []string{"mtime"},
		},
		{
			name: "a write that creates a file", existing: false,
			want: func(remotefile.Info) remotefile.Attributes { return remotefile.Attributes{Mode: "0640"} },
			predict: func(want remotefile.Attributes, before remotefile.Info, _ string) remotefile.Prediction {
				return remotefile.PredictWrite(want, before, int64(len(content)))
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, _ string) error {
				return remotefile.Write(ctx, conn, path, content)
			},
			unknown: []string{"owner", "group", "mtime"},
		},
		{
			name: "a touch of a file that exists", existing: true,
			want: func(remotefile.Info) remotefile.Attributes { return remotefile.Attributes{Mode: "0600"} },
			predict: func(want remotefile.Attributes, before remotefile.Info, _ string) remotefile.Prediction {
				return remotefile.PredictTouch(want, before)
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, _ string) error {
				return remotefile.Touch(ctx, conn, path)
			},
			unknown: []string{"mtime"},
		},
		{
			name: "a touch that creates a file", existing: false,
			want: func(remotefile.Info) remotefile.Attributes { return remotefile.Attributes{Mode: "0600"} },
			predict: func(want remotefile.Attributes, before remotefile.Info, _ string) remotefile.Prediction {
				return remotefile.PredictTouch(want, before)
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, _ string) error {
				return remotefile.Touch(ctx, conn, path)
			},
			unknown: []string{"owner", "group", "size", "mtime"},
		},
		{
			name: "a symbolic link", existing: false,
			want: func(remotefile.Info) remotefile.Attributes { return remotefile.Attributes{} },
			predict: func(_ remotefile.Attributes, _ remotefile.Info, target string) remotefile.Prediction {
				return remotefile.PredictSymlink(target)
			},
			op: func(ctx context.Context, conn *remoteexec.Conn, path, target string) error {
				return remotefile.Symlink(ctx, conn, target, path)
			},
			unknown: []string{"mode", "owner", "group", "size", "mtime"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn := connect(t)
			path := filepath.Join(t.TempDir(), "target")
			target := filepath.Join(t.TempDir(), "elsewhere")
			if tt.existing {
				path = predictFixture(t, remotefile.KindFile, existingMode)
			}

			before := statOf(t, path)
			want := tt.want(before)
			predicted := tt.predict(want, before, target).Map()

			if err := tt.op(ctx, conn, path, target); err != nil {
				t.Fatalf("the operation: %v", err)
			}
			if !reflect.DeepEqual(want, remotefile.Attributes{}) {
				if _, err := remotefile.Apply(ctx, conn, path, want, statOf(t, path)); err != nil {
					t.Fatalf("Apply after the operation: %v", err)
				}
			}
			actual := statOf(t, path).Map()

			for _, key := range tt.unknown {
				if _, claimed := predicted[key]; claimed {
					t.Errorf("the prediction claims %s = %v, which only the device decides here", key, predicted[key])
				}
				delete(actual, key)
			}
			if !reflect.DeepEqual(predicted, actual) {
				t.Errorf("predicted %v, but the operation left %v", predicted, actual)
			}
		})
	}
}

// TestPredictSymlink_NamesATargetThatDoesNotExist pins that a link's
// prediction is its target as written, not the target resolved: ln -s
// records the string it was given whether or not anything is there, and a
// check that resolved it would disagree with the run it previews.
func TestPredictSymlink_NamesATargetThatDoesNotExist(t *testing.T) {
	target := filepath.Join(t.TempDir(), "not-there")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("the target %s exists, so this test proves nothing: %v", target, err)
	}
	got := remotefile.PredictSymlink(target).Map()
	want := map[string]any{"exists": true, "kind": "symlink", "target": target}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PredictSymlink(%s) = %v, want %v", target, got, want)
	}
}
