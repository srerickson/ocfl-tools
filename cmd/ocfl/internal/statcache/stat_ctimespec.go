//go:build darwin || freebsd || netbsd

package statcache

import "syscall"

// sysStat sets the platform-specific fields of st from sys, the value of
// [fs.FileInfo.Sys].
func sysStat(sys any, st *Stat) {
	stat, ok := sys.(*syscall.Stat_t)
	if !ok || stat == nil {
		return
	}
	st.Ctime = stat.Ctimespec.Nano()
	st.Ino = uint64(stat.Ino)
	st.Dev = uint64(stat.Dev)
	st.UID = stat.Uid
	st.GID = stat.Gid
}
