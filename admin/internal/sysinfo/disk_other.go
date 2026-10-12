//go:build !linux

package sysinfo

// diskUsage: fuori da Linux (l'host di GitStack è sempre Linux) il disco
// risulta «non disponibile».
func diskUsage(string) (total, free int64) { return 0, 0 }
