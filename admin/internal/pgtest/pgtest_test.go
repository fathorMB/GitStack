package pgtest

import (
	"testing"
	"time"
)

// Con l'immagine non in locale `docker run -d` stampa prima il pull: l'id è
// l'ultima riga (GIT-217).
func TestContainerID_IgnoraLeRigheDelPull(t *testing.T) {
	out := []byte("Unable to find image 'postgres:15-alpine' locally\n" +
		"15-alpine: Pulling from library/postgres\n" +
		"Status: Downloaded newer image for postgres:15-alpine\n" +
		"3f2a9c1d7b0e4a5f8c6d\n")
	if got := containerID(out); got != "3f2a9c1d7b0e4a5f8c6d" {
		t.Fatalf("containerID = %q", got)
	}
	if got := containerID([]byte("abc123\n")); got != "abc123" {
		t.Fatalf("containerID senza pull = %q", got)
	}
}

func TestStaleIDs_SoloPiuVecchiDiUnOra(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	out := "vecchio 2026-10-26 10:30:00 +0000 UTC\n" +
		"recente 2026-10-26 11:30:00 +0000 UTC\n" +
		"fuso 2026-10-26 13:00:00 +0200 CEST\n" +
		"rotto non-una-data\n\n"
	got := staleIDs(out, now)
	if len(got) != 1 || got[0] != "vecchio" {
		t.Fatalf("staleIDs = %v, attesi solo [vecchio]", got)
	}
}
