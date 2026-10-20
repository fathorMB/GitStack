//go:build !linux

package main

// hardenProcess: solo Linux ha PR_SET_DUMPABLE; il runner gira nel pod Linux.
func hardenProcess() error { return nil }
