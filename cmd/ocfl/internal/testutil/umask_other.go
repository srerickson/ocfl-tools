//go:build !unix

package testutil

import "testing"

// SetUmask skips the test t: the umask is only supported on Unix.
func SetUmask(t testing.TB, mask int) {
	t.Helper()
	t.Skip("umask is only supported on Unix")
}
