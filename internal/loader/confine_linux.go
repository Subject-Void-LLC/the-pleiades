//go:build linux

// Package loader: confinement on Linux, through Landlock.
package loader

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock's filesystem access rights, grouped by the first ABI version
// that has them. The kernel refuses a right it does not know, so the set
// a ruleset handles is built from the running kernel's version.
const (
	// fsRightsV1 is every right ABI 1 has: execute, write, read, list,
	// remove a directory or file, and make each kind of file.
	fsRightsV1 = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM

	// fileRights are the rights that make sense on a single file rather
	// than a directory. A rule on a file may grant only these.
	fileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE |
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// probeLandlock asks the kernel which Landlock ABI version it offers. It
// is a variable so a test can stand in for a kernel without Landlock.
var probeLandlock = func() (int, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(abi), nil
}

// confinementAvailable returns the Landlock ABI version the kernel
// offers, or an error saying why programs cannot be confined here.
func confinementAvailable() (int, error) {
	abi, err := probeLandlock()
	if err != nil || abi < 1 {
		reason := "it reports no version"
		if err != nil {
			reason = err.Error()
		}
		return 0, fmt.Errorf("this kernel does not offer Landlock (%s), which confines each program to what it was handed; "+
			"external Collections are refused rather than run unconfined (Landlock needs Linux 5.13 or later, enabled in the kernel's lsm= list)", reason)
	}
	return abi, nil
}

// protectProcess marks this process as not dumpable. Its memory and its
// starting environment under /proc then belong to root, so a program it
// starts, which runs as the same user, cannot read the master key or the
// broker's credentials out of them (FAILURE_PATTERNS 251). The one cost
// is that this process leaves no core file and a debugger cannot attach
// to it, which is why Load does this only when it is about to start a
// program.
func protectProcess() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("failed to mark this process not dumpable: %w", err)
	}
	return nil
}

// handledRights is every filesystem right the kernel at abi knows. A
// ruleset handling all of them denies each one everywhere no rule grants
// it.
func handledRights(abi int) uint64 {
	rights := uint64(fsRightsV1)
	if abi >= 2 {
		rights |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		rights |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		rights |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return rights
}

// scopes is the IPC scoping the kernel at abi offers: from ABI 6 a
// program can neither signal a process outside its domain, such as
// Pleiades itself, nor connect to an abstract Unix socket created outside
// it.
func scopes(abi int) uint64 {
	if abi >= 6 {
		return unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
	}
	return 0
}

// rights turns an access class into Landlock rights.
func (a accessClass) rights() uint64 {
	read := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	switch a {
	case readExec:
		return read | unix.LANDLOCK_ACCESS_FS_EXECUTE
	case readOnly:
		return read
	case device:
		return unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
			unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	default:
		return ^uint64(0)
	}
}

// startConfined starts cmd with the program confined to rules.
//
// Landlock restricts the thread that asks for it, and a new process
// inherits the restriction of the thread that started it. So a goroutine
// locks itself to an OS thread, restricts that thread, and starts the
// program from it (confineAndStart). The thread is never unlocked: a
// goroutine that exits while locked takes its thread with it, so no other
// goroutine ever runs on a confined thread. The Go runtime also never
// clones a locked thread to make a new one (it asks its template thread
// instead), so the restriction cannot spread to the rest of the process
// that way either.
func startConfined(cmd *exec.Cmd, rules []confineRule, abi int) error {
	errc := make(chan error, 1)
	go confineAndStart(cmd, rules, abi, errc)
	return <-errc
}

// confineAndStart is startConfined's goroutine. It never restricts the
// process's main thread. That thread cannot exit, so Go parks it forever
// instead, and it is the thread a signal to this process's ID is checked
// against: restricting it would put Pleiades inside the program's own
// domain, where the program may signal it. Found by
// TestConfinement_AProgramReachesOnlyWhatItWasHanded, whose program could
// signal its parent whenever the scheduler happened to pick that thread.
// On the main thread this goroutine holds it, so the goroutine it starts
// cannot be scheduled there, and passes the work on.
func confineAndStart(cmd *exec.Cmd, rules []confineRule, abi int, errc chan<- error) {
	runtime.LockOSThread()
	if unix.Gettid() == unix.Getpid() {
		inner := make(chan error, 1)
		go confineAndStart(cmd, rules, abi, inner)
		err := <-inner
		runtime.UnlockOSThread()
		errc <- err
		return
	}
	errc <- restrictThreadAndStart(cmd, rules, abi)
}

// restrictThreadAndStart builds the Landlock ruleset for rules, restricts
// the calling thread to it, and starts cmd. It must run on a thread that
// is locked and will never be unlocked (startConfined).
func restrictThreadAndStart(cmd *exec.Cmd, rules []confineRule, abi int) error {
	handled := handledRights(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled, Scoped: scopes(abi)}
	// #nosec G103 -- the Landlock system calls take a pointer to a kernel
	// structure; attr is a local that outlives the call, and x/sys defines
	// its layout from the kernel's own header.
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("failed to create the program's Landlock ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer func() { _ = unix.Close(ruleset) }()

	for _, r := range rules {
		if err := addRule(ruleset, r, handled); err != nil {
			return err
		}
	}

	// Landlock refuses to restrict a thread that could still gain
	// privileges by executing a setuid program. This applies to this
	// thread and what it starts, never to the rest of the process.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("failed to set no_new_privs for the program: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("failed to apply the program's Landlock ruleset: %w", errno)
	}
	return cmd.Start()
}

// addRule grants r's access beneath r.path in ruleset. An optional path
// that does not exist is skipped; any other failure refuses the run.
func addRule(ruleset int, r confineRule, handled uint64) error {
	pathFD, err := unix.Open(r.path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if !r.required && errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("failed to open %s for the program's confinement: %w", r.path, err)
	}
	defer func() { _ = unix.Close(pathFD) }()

	var st unix.Stat_t
	if err := unix.Fstat(pathFD, &st); err != nil {
		return fmt.Errorf("failed to stat %s for the program's confinement: %w", r.path, err)
	}
	access := r.access.rights() & handled
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		access &= fileRights
	}

	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(pathFD)} // #nosec G115 -- a file descriptor always fits in 32 bits.
	// #nosec G103 -- as in restrictThreadAndStart: a pointer to a local
	// kernel structure for the duration of one system call.
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("failed to allow %s to the program: %w", r.path, errno)
	}
	return nil
}
