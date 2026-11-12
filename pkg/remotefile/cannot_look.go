// What a probe answers when its test found nothing at a path: absent, or
// that it could not look. A shell's test -e, -L and -f answer false both
// for a path that is not there and for one under a directory the login may
// not search, so "false" alone cannot be read as "not there" (Phase 131,
// R4: could not look is never absent).
package remotefile

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// statusUnsearchable means a probe found nothing at a path because a
// directory on the way to it is closed to the login, which is a failure:
// it says nothing about whether the path is there.
const statusUnsearchable = 123

// ErrCannotLook is a probe's answer for a path it could not look at: a
// directory on the way to it exists and the login may not search it, so
// whether anything is at the path is unknown. It is never reported as
// absent, since a method that read it as absent would create over what it
// could not see, and a lookup that did would tell an agent a thing is not
// there when it may be.
var ErrCannotLook = errors.New("cannot look")

// absentOrUnsearchable is the shell a probe runs once its own test found
// nothing at quoted (a path already quoted with remoteexec.QuoteArg). It
// walks up from the path to the nearest ancestor that exists: one the login
// may search (or a file, under which nothing can be) means the path is not
// there, and the probe exits statusMissing; a directory it may not search
// means it could not look, and the probe prints that directory and exits
// statusUnsearchable. The walk ends at "/" or ".", which dirname returns
// unchanged. Only POSIX test and dirname run, so a BSD, macOS or BusyBox
// device answers the same.
func absentOrUnsearchable(quoted string) string {
	return "p=" + quoted + "; while :; do d=$(dirname \"$p\"); " +
		"if [ -e \"$d\" ]; then " +
		"if [ -d \"$d\" ] && [ ! -x \"$d\" ]; then printf '%s' \"$d\"; exit " + strconv.Itoa(statusUnsearchable) + "; fi; " +
		"exit " + strconv.Itoa(statusMissing) + "; fi; " +
		"if [ \"$d\" = \"$p\" ]; then exit " + strconv.Itoa(statusMissing) + "; fi; " +
		"p=$d; done"
}

// cannotLook is op's error for path when its probe exited
// statusUnsearchable, naming the directory the probe printed.
func cannotLook(op, path, dir string) error {
	return fmt.Errorf("%s %s: %w: the login may not search the directory %s, so whether anything is there is unknown", op, path, ErrCannotLook, farside.Output(dir))
}
