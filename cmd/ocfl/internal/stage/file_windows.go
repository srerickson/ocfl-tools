package stage

import (
	"errors"
	"io/fs"
	"syscall"
)

// errorSharingViolation is the Windows error ERROR_SHARING_VIOLATION: the file
// is open in another process.
const errorSharingViolation syscall.Errno = 32

// lockBusy returns true if err, from creating a stage file's lock file with
// O_EXCL, means that another command holds the lock: the lock file exists.
// On Windows, creating a file that is being deleted fails with
// ERROR_ACCESS_DENIED, which means that the lock is being released.
func lockBusy(err error) bool {
	return errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}

// removeBusy returns true if err, from removing a lock file, may be temporary:
// on Windows, a file that's open in another process, such as a command reading
// the lock file to report who holds it, can't be removed.
func removeBusy(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
