//go:build linux

package sysinfo

import "syscall"

// diskUsage dà dimensione e spazio libero (per un utente non privilegiato,
// come `df --output=avail`) del filesystem di path; 0, 0 se non si legge.
func diskUsage(path string) (total, free int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := int64(st.Bsize)                               //nolint:unconvert // il tipo varia con l'architettura
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs //nolint:unconvert
}
