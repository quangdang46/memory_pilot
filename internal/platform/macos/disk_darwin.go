//go:build darwin

package macos

import "syscall"

// DiskHeadroom: boot-volume free/total via statfs, feeds swap risk (bead gk9.1).
func (i *Impl) DiskHeadroom() (freeBytes, totalBytes uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, true
}
