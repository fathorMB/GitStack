//go:build !windows

package main

// protectFile: su Unix il modo 0600 con cui si scrive il file basta.
func protectFile(string) error { return nil }
