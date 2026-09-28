package core

import "testing"

// TestPlaceholder verifica solo che il modulo sia compilabile e testabile
// dalla pipeline CI. I test veri arrivano con l'implementazione del
// servizio (vedi services/README.md).
func TestPlaceholder(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("l'aritmetica di base è rotta")
	}
}
