//go:build unix

package backup

import "syscall"

// diskFree è lo spazio libero (per un utente non privilegiato) del filesystem di dir.
func diskFree(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:gosec // valori del kernel, lontani da overflow
}
