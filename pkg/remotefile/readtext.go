// Package remotefile: reading a regular text file whole, within a bound,
// for a caller that parses what it reads.
package remotefile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// ReadText returns the contents of the regular file at filePath, which
// must be UTF-8 text of at most max bytes, with what Stat found there.
//
// Unlike Read it treats every shape but one as a refusal, because its
// callers parse what it returns and act on the result: an absent path is
// an error (a file a task names must be there), anything that is not a
// regular file is an error (a symlink would be replaced by a later Write,
// not written through, and a fifo would block the read), a file over max
// is an error rather than a prefix (a truncated manifest parses as a
// different, shorter one), and bytes that are not UTF-8 are an error
// rather than text with replacement characters in it.
func ReadText(ctx context.Context, conn *remoteexec.Conn, filePath string, max int) ([]byte, Info, error) {
	info, err := Stat(ctx, conn, filePath)
	if err != nil {
		return nil, info, err
	}
	switch {
	case !info.Exists():
		return nil, info, fmt.Errorf("%s does not exist on the device", filePath)
	case info.Kind != KindFile:
		return nil, info, fmt.Errorf("%s is a %s, not a regular file", filePath, info.Kind)
	}
	res, err := conn.RunWithStdinBounded(ctx, remoteexec.QuoteCommand([]string{"cat", "--", filePath}), nil, max)
	if errors.Is(err, remoteexec.ErrOutputTooLarge) {
		return nil, info, fmt.Errorf("%s is over the %d bytes this reads", filePath, max)
	}
	if err != nil {
		return nil, info, fmt.Errorf("reading %s: %w", filePath, err)
	}
	if res.ExitCode != 0 {
		return nil, info, fmt.Errorf("could not read %s: %s", filePath, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	content := []byte(res.Stdout)
	if !utf8.Valid(content) {
		return nil, info, fmt.Errorf("%s is not UTF-8 text", filePath)
	}
	return content, info, nil
}
