//go:build !unix

package config

import "io/fs"

// checkMode: fuori da Unix i permessi POSIX non esistono, `gitstack` è uno
// strumento dell'host Linux e questo ramo serve solo a compilare altrove.
func checkMode(fs.FileMode) error { return nil }
