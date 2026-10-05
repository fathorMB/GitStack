//go:build unix

package config

import (
	"fmt"
	"io/fs"
)

// checkMode rifiuta un file accessibile a gruppo o altri.
func checkMode(m fs.FileMode) error {
	if m.Perm()&0o077 != 0 {
		return fmt.Errorf("permessi %04o troppo larghi: il file deve essere root-only (chmod 600)", m.Perm())
	}
	return nil
}
