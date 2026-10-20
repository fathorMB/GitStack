//go:build linux

package main

import "golang.org/x/sys/unix"

// hardenProcess rende il processo non "dumpable": lo stesso uid non può leggere
// /proc/<pid>/environ, dove sta il segreto dei runner. act è un figlio e dopo execve
// torna dumpable, ma non ha il segreto (l'ambiente è filtrato in cirunner).
func hardenProcess() error {
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
