//go:build !(linux || openbsd || dragonfly || solaris || darwin || freebsd || netbsd)

package statcache

// sysStat does nothing: the platform-specific fields of [Stat] aren't
// available on this platform (including Windows, where [fs.FileInfo.Sys]
// doesn't have a file index or change time), so they aren't compared.
func sysStat(sys any, st *Stat) {}
