//go:build unix

package backup

import "os"

func lchown(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil // senza root il proprietario resta quello di chi estrae
	}
	return os.Lchown(path, uid, gid)
}
