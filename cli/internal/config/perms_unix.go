//go:build !windows

package config

import "os"

// restrict porta i permessi a 0600 (file) o 0700 (cartelle).
func restrict(path string, dir bool) error {
	mode := os.FileMode(0o600)
	if dir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}
