package pgtest

import "testing"

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
