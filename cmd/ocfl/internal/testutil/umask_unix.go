//go:build unix

package testutil

import (
	"syscall"
	"testing"
)

// SetUmask sets the process's umask to mask for the rest of the test t, then
// restores it. The umask is process-wide: tests that call SetUmask must not
// run in parallel.
func SetUmask(t testing.TB, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}
