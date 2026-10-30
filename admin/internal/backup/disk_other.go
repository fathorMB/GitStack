//go:build !unix

package backup

// diskFree: fuori da unix (l'host di GitStack è sempre Linux) lo spazio libero non si legge.
func diskFree(string) (int64, bool) { return 0, false }
