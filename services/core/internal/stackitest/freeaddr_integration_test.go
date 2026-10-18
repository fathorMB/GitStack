//go:build integration

package stackitest

import "testing"

// TestFreeAddrDistinti: freeAddr non dà mai due volte lo stesso indirizzo
// nello stesso processo (GIT-255). Non serve Docker.
func TestFreeAddrDistinti(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		a := freeAddr(t)
		if seen[a] {
			t.Fatalf("indirizzo ripetuto: %s (iterazione %d)", a, i)
		}
		seen[a] = true
	}
}
