package telemetry

import "io"

// ConfigWithStdoutWriterForTest returns a copy of cfg whose stdout
// exporter writes to w instead of os.Stdout.
//
// The field is unexported and this seam lives in an _test.go file so it
// exists only when the package's own tests are compiled: a production
// caller has no way to redirect exported spans, which is what keeps
// "stdout" a truthful name for the deployment target rather than a
// configurable one.
func ConfigWithStdoutWriterForTest(cfg Config, w io.Writer) Config {
	cfg.stdout = w
	return cfg
}
