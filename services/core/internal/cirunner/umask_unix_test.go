//go:build !windows

package cirunner

import "syscall"

// setUmask imposta la umask del processo e restituisce la funzione che la ripristina.
func setUmask(m int) func() {
	old := syscall.Umask(m)
	return func() { syscall.Umask(old) }
}
