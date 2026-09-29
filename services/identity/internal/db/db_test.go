package db

import (
	"context"
	"strings"
	"testing"
)

func TestOpen_NoPasswordInError(t *testing.T) {
	// Verifica che la password non compaia mai nel messaggio di errore
	// di Open, nemmeno incapsulata nel %w di fmt.Errorf.
	password := "super-secret-pw-12345"
	dsn := "postgres://identity:" + password + "@127.0.0.1:5999/gitstack"

	ctx := context.Background()
	_, err := Open(ctx, dsn, 1)
	if err == nil {
		t.Fatal("attendevo un errore da Open")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("la password %q è nel messaggio di errore: %s", password, err.Error())
	}
}
