//go:build !unix

package backup

func lchown(string, int, int) error { return nil }
