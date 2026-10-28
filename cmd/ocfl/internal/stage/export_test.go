package stage

import (
	"testing"
	"time"
)

// SetLockWait sets how long a stage file lock is waited for, until t's
// cleanup.
func SetLockWait(t testing.TB, d time.Duration) {
	old := lockWait
	lockWait = d
	t.Cleanup(func() { lockWait = old })
}
