//go:build !windows

package stage

import (
	"errors"
	"io/fs"
)

// lockBusy returns true if err, from creating a stage file's lock file with
// O_EXCL, means that another command holds the lock: the lock file exists.
func lockBusy(err error) bool { return errors.Is(err, fs.ErrExist) }

// removeBusy returns true if err, from removing a lock file, may be temporary.
// Removing an open file doesn't fail on Unix, so it never is.
func removeBusy(error) bool { return false }
